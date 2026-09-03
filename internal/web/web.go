package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

const Address = "127.0.0.1:8080"

//go:embed templates/*.html
var templateFiles embed.FS

type Runner interface {
	CreateSession(context.Context, string) (int64, string, error)
	Run(context.Context, int64, harness.Options, *state.ResumeTarget) (int64, error)
}

type Server struct {
	ctx       context.Context
	store     *state.Store
	runner    Runner
	templates map[string]*template.Template
	logOutput io.Writer
	pendingMu sync.Mutex
	pending   map[int64]pendingTurn
}

// the same struct is used by all the templates
type pageData struct {
	Title        string
	Sessions     []sessionListItem
	Session      state.SessionRecord
	Messages     []conversationMessage
	Events       []eventView
	SessionCount int
	SessionURL   string
	Pending      string
	Failure      string
}

type pendingTurn struct {
	Prompt  string
	Failure string
}

type sessionListItem struct {
	state.SessionRecord
	URL     string
	Preview string
}

type conversationMessage struct {
	Role string
	Text string
}

type eventView struct {
	Method     string
	ParamsJSON string
	ReceivedAt string
}

func New(ctx context.Context, store *state.Store, runner Runner) (*Server, error) {
	templates := make(map[string]*template.Template)
	for _, name := range []string{"sessions", "new-session", "session"} {
		parsed, err := template.ParseFS(
			templateFiles,
			"templates/base.html",
			"templates/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		templates[name] = parsed
	}
	return &Server{
		ctx:       ctx,
		store:     store,
		runner:    runner,
		templates: templates,
		logOutput: io.Discard,
		pending:   make(map[int64]pendingTurn),
	}, nil
}

func (s *Server) ListenAndServe(logOutput io.Writer) error {
	s.logOutput = logOutput
	server := &http.Server{
		Addr:              Address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Fprintf(logOutput, "Pact web interface listening on http://%s\n", Address)
	return server.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sessions", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /sessions", s.listSessions)
	mux.HandleFunc("GET /sessions/new", s.newSession)
	mux.HandleFunc("POST /sessions", s.startSession)
	mux.HandleFunc("GET /sessions/{sessionID}", s.showSession)
	mux.HandleFunc("POST /sessions/{sessionID}/messages", s.sendMessage)
	return mux
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]sessionListItem, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, sessionListItem{
			SessionRecord: session,
			URL:           sessionURL(session.ID),
			Preview:       listPreview(session.LastAgentMessage),
		})
	}
	s.render(w, "sessions", pageData{
		Title:        "Sessions",
		Sessions:     items,
		SessionCount: len(items),
	})
}

func (s *Server) newSession(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "new-session", pageData{Title: "New session"})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	prompt, ok := promptFromForm(w, r)
	if !ok {
		return
	}
	workspace, err := os.MkdirTemp("", "pact-session-")
	if err != nil {
		http.Error(w, fmt.Sprintf("create session workspace: %v", err), http.StatusInternalServerError)
		return
	}
	sessionID, workspace, err := s.runner.CreateSession(r.Context(), workspace)
	if err != nil {
		os.Remove(workspace)
		http.Error(w, fmt.Sprintf("create session: %v", err), http.StatusInternalServerError)
		return
	}
	options := harness.DefaultOptions()
	options.Workspace = workspace
	options.Prompt = prompt
	s.beginTurn(sessionID, prompt)
	go s.runTurn(sessionID, options, nil)
	http.Redirect(w, r, sessionURL(sessionID), http.StatusSeeOther)
}

func (s *Server) runTurn(sessionID int64, options harness.Options, target *state.ResumeTarget) {
	_, err := s.runner.Run(s.ctx, sessionID, options, target)
	s.finishTurn(sessionID, err)
	if err != nil {
		fmt.Fprintf(s.logOutput, "run session %d: %v\n", sessionID, err)
	}
}

func (s *Server) showSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := sessionIDFromRequest(w, r)
	if !ok {
		return
	}
	session, err := s.store.GetSession(r.Context(), sessionID)
	if errors.Is(err, state.ErrSessionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	events, err := s.store.ListSessionEvents(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	messages, err := conversationMessages(session.TranscriptJSON)
	if err != nil {
		http.Error(w, fmt.Sprintf("decode session transcript: %v", err), http.StatusInternalServerError)
		return
	}
	eventViews := make([]eventView, 0, len(events))
	for _, event := range events {
		eventViews = append(eventViews, eventView{
			Method:     event.Method,
			ParamsJSON: prettyJSON(event.ParamsJSON),
			ReceivedAt: event.ReceivedAt,
		})
	}
	pending := s.pendingTurn(sessionID)
	s.render(w, "session", pageData{
		Title:      fmt.Sprintf("Session %d", session.ID),
		Session:    session,
		Messages:   messages,
		Events:     eventViews,
		SessionURL: sessionURL(sessionID),
		Pending:    pending.Prompt,
		Failure:    pending.Failure,
	})
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	prompt, ok := promptFromForm(w, r)
	if !ok {
		return
	}
	sessionID, ok := sessionIDFromRequest(w, r)
	if !ok {
		return
	}
	session, err := s.store.GetSession(r.Context(), sessionID)
	if errors.Is(err, state.ErrSessionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !s.beginTurn(sessionID, prompt) {
		http.Error(w, "a turn is already running for this session", http.StatusConflict)
		return
	}
	options := harness.DefaultOptions()
	options.Workspace = session.WorkspaceDir
	options.Prompt = prompt
	var target *state.ResumeTarget
	resumeTarget, err := s.store.GetResumeTarget(r.Context(), sessionID)
	if err == nil {
		target = &resumeTarget
		options.Model = resumeTarget.Model
		options.Effort = resumeTarget.Effort
		options.Image = resumeTarget.DockerfileVariant
	} else if !errors.Is(err, state.ErrResumeTargetNotFound) {
		s.finishTurn(sessionID, nil)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go s.runTurn(sessionID, options, target)
	http.Redirect(w, r, sessionURL(sessionID), http.StatusSeeOther)
}

func (s *Server) beginTurn(sessionID int64, prompt string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if pending, ok := s.pending[sessionID]; ok && pending.Failure == "" {
		return false
	}
	s.pending[sessionID] = pendingTurn{Prompt: prompt}
	return true
}

func (s *Server) finishTurn(sessionID int64, err error) {
	s.pendingMu.Lock()
	if err == nil {
		delete(s.pending, sessionID)
	} else {
		pending := s.pending[sessionID]
		pending.Failure = err.Error()
		s.pending[sessionID] = pending
	}
	s.pendingMu.Unlock()
}

func (s *Server) pendingTurn(sessionID int64) pendingTurn {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pending[sessionID]
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates[name].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, fmt.Sprintf("render page: %v", err), http.StatusInternalServerError)
	}
}

func promptFromForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return "", false
	}
	prompt := strings.TrimSpace(r.FormValue("message"))
	if prompt == "" {
		http.Error(w, "message must not be empty", http.StatusBadRequest)
		return "", false
	}
	return prompt, true
}

func sessionIDFromRequest(w http.ResponseWriter, r *http.Request) (int64, bool) {
	sessionID, err := strconv.ParseInt(r.PathValue("sessionID"), 10, 64)
	if err != nil || sessionID < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return sessionID, true
}

func sessionURL(sessionID int64) string {
	return "/sessions/" + url.PathEscape(strconv.FormatInt(sessionID, 10))
}

func listPreview(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, message)
	characters := []rune(message)
	if len(characters) > 50 {
		characters = characters[:50]
	}
	return string(characters)
}

func conversationMessages(transcript json.RawMessage) ([]conversationMessage, error) {
	if len(transcript) == 0 {
		return nil, nil
	}
	var document struct {
		Thread struct {
			Turns []struct {
				Items []json.RawMessage `json:"items"`
			} `json:"turns"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(transcript, &document); err != nil {
		return nil, err
	}

	var messages []conversationMessage
	for _, turn := range document.Thread.Turns {
		for _, rawItem := range turn.Items {
			var item struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Phase   string `json:"phase"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(rawItem, &item); err != nil {
				return nil, err
			}
			switch item.Type {
			case "userMessage":
				var textParts []string
				for _, content := range item.Content {
					if content.Type == "text" && content.Text != "" {
						textParts = append(textParts, content.Text)
					}
				}
				if len(textParts) != 0 {
					messages = append(messages, conversationMessage{Role: "You", Text: strings.Join(textParts, "\n")})
				}
			case "agentMessage":
				if item.Text == "" {
					continue
				}
				role := "Agent"
				if item.Phase == "commentary" {
					role = "Agent commentary"
				}
				messages = append(messages, conversationMessage{Role: role, Text: item.Text})
			}
		}
	}
	return messages, nil
}

func prettyJSON(raw json.RawMessage) string {
	var output bytes.Buffer
	if err := json.Indent(&output, raw, "", "  "); err != nil {
		return string(raw)
	}
	return output.String()
}
