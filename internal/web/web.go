package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/starfederation/datastar-go/datastar"
)

const (
	Address = "127.0.0.1:8080"
)

type Runner interface {
	CreateSession(context.Context, string) (int64, string, error)
	Run(context.Context, int64, harness.Options, *state.ResumeTarget) (int64, error)
}

type Server struct {
	ctx       context.Context
	store     *state.Store
	runner    Runner
	templates *templates
	static    http.Handler
	logger    *slog.Logger
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
	Done    chan struct{}
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
	static, err := staticFS()
	if err != nil {
		return nil, fmt.Errorf("open static files: %w", err)
	}

	templates, err := newTemplates()
	if err != nil {
		return nil, fmt.Errorf("open templates: %w", err)
	}

	return &Server{
		ctx:       ctx,
		store:     store,
		runner:    runner,
		templates: templates,
		static:    http.FileServerFS(static),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		pending:   make(map[int64]pendingTurn),
	}, nil
}

func (s *Server) ListenAndServe(logOutput io.Writer) error {
	s.logger = slog.New(slog.NewTextHandler(logOutput, nil))
	server := &http.Server{
		Addr:              Address,
		Handler:           s.Handler(),
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.logger.Info("Pact web interface listening", "address", "http://"+Address)
	return server.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sessions", http.StatusSeeOther)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStaticFiles(s.static)))
	mux.HandleFunc("GET /sessions", s.listSessions)
	mux.HandleFunc("GET /sessions/new", s.newSession)
	mux.HandleFunc("POST /sessions", s.startSession)
	mux.HandleFunc("GET /sessions/{sessionID}", s.showSession)
	mux.HandleFunc("POST /sessions/{sessionID}/messages", s.sendMessage)
	mux.HandleFunc("GET /sessions/{sessionID}/chat", s.streamSessionChat)
	return mux
}

func cacheStaticFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Debug != "true" {
			w.Header().Set("Cache-Control", "public, max-age=600")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		s.serverError(w, r, "list sessions", err)
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
	s.render(w, r, "sessions", pageData{
		Title:        "Sessions",
		Sessions:     items,
		SessionCount: len(items),
	})
}

func (s *Server) newSession(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "new-session", pageData{Title: "New session"})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	prompt, ok := promptFromForm(w, r)
	if !ok {
		return
	}
	workspaceDir, err := os.MkdirTemp("", "pact-session-")
	if err != nil {
		s.serverError(w, r, "create session workspace", err)
		return
	}
	sessionID, workspace, err := s.runner.CreateSession(r.Context(), workspaceDir)
	if err != nil {
		if removeErr := os.Remove(workspaceDir); removeErr != nil {
			s.logger.ErrorContext(r.Context(), "remove failed session workspace", requestLogAttrs(r, "error", removeErr)...)
		}
		s.serverError(w, r, "create session", err)
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
	runID, err := s.runner.Run(s.ctx, sessionID, options, target)
	s.finishTurn(sessionID, err)
	if err != nil {
		s.logger.ErrorContext(s.ctx, "run session", "session_id", sessionID, "run_id", runID, "error", err)
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
		s.serverError(w, r, "get session", err, "session_id", sessionID)
		return
	}
	events, err := s.store.ListSessionEvents(r.Context(), sessionID)
	if err != nil {
		s.serverError(w, r, "list session events", err, "session_id", sessionID)
		return
	}
	messages, err := conversationMessages(session.TranscriptJSON)
	if err != nil {
		s.serverError(w, r, "decode session transcript", err, "session_id", sessionID)
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
	s.render(w, r, "session", pageData{
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
		s.serverError(w, r, "get session", err, "session_id", sessionID)
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
		s.serverError(w, r, "get resume target", err, "session_id", sessionID)
		return
	}
	go s.runTurn(sessionID, options, target)
	if r.Header.Get("Datastar-Request") == "true" {
		http.Redirect(w, r, sessionURL(sessionID)+"/chat?clear-message=true", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, sessionURL(sessionID), http.StatusSeeOther)
}

func (s *Server) streamSessionChat(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := sessionIDFromRequest(w, r)
	if !ok {
		return
	}

	tmpl, err := s.templates.get("session")
	if err != nil {
		s.serverError(w, r, "load session template", err)
		return
	}

	data, done, err := s.sessionChatData(r.Context(), sessionID)
	if errors.Is(err, state.ErrSessionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load session chat", err, "session_id", sessionID)
		return
	}

	sse := datastar.NewSSE(w, r)

	if r.URL.Query().Has("clear-message") {
		if err := sse.PatchSignals([]byte(`{message: ''}`)); err != nil {
			s.logger.ErrorContext(r.Context(), "clear message", "session_id", sessionID, "error", err)
			return
		}
	}

	for {
		var fragment bytes.Buffer
		if err := tmpl.ExecuteTemplate(&fragment, "session-chat", data); err != nil {
			s.logger.ErrorContext(r.Context(), "render session chat", "session_id", sessionID, "error", err)
			return
		}
		if err := sse.PatchElements(fragment.String()); err != nil {
			s.logger.ErrorContext(r.Context(), "patch session chat", "session_id", sessionID, "error", err)
			return
		}
		if done == nil || data.Failure != "" {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-done:
		}

		data, done, err = s.sessionChatData(r.Context(), sessionID)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "refresh session chat", "session_id", sessionID, "error", err)
			return
		}
	}
}

func (s *Server) sessionChatData(ctx context.Context, sessionID int64) (pageData, <-chan struct{}, error) {
	pending := s.pendingTurn(sessionID)
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return pageData{}, nil, err
	}
	messages, err := conversationMessages(session.TranscriptJSON)
	if err != nil {
		return pageData{}, nil, fmt.Errorf("decode session transcript: %w", err)
	}
	return pageData{
		Messages: messages,
		Pending:  pending.Prompt,
		Failure:  pending.Failure,
	}, pending.Done, nil
}

func (s *Server) beginTurn(sessionID int64, prompt string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if pending, ok := s.pending[sessionID]; ok && pending.Failure == "" {
		return false
	}
	s.pending[sessionID] = pendingTurn{Prompt: prompt, Done: make(chan struct{})}
	return true
}

func (s *Server) finishTurn(sessionID int64, err error) {
	s.pendingMu.Lock()
	pending := s.pending[sessionID]
	if err == nil {
		delete(s.pending, sessionID)
	} else {
		pending.Failure = err.Error()
		s.pending[sessionID] = pending
	}
	s.pendingMu.Unlock()
	if pending.Done != nil {
		close(pending.Done)
	}
}

func (s *Server) pendingTurn(sessionID int64) pendingTurn {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pending[sessionID]
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	t, err := s.templates.get(name)
	if err != nil {
		s.serverError(w, r, "load page template", err, "template", name)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		s.logger.ErrorContext(r.Context(), "render page", requestLogAttrs(r, "template", name, "error", err)...)
	}
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, operation string, err error, attrs ...any) {
	attrs = append(attrs, "error", err)
	s.logger.ErrorContext(r.Context(), operation, requestLogAttrs(r, attrs...)...)
	http.Error(w, fmt.Sprintf("%s: %v", operation, err), http.StatusInternalServerError)
}

func requestLogAttrs(r *http.Request, attrs ...any) []any {
	return append([]any{"method", r.Method, "path", r.URL.Path}, attrs...)
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

func (s *Server) sessionPageData(ctx context.Context, sessionID int64) (pageData, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return pageData{}, err
	}
	events, err := s.store.ListSessionEvents(ctx, sessionID)
	if err != nil {
		return pageData{}, err
	}
	messages, err := conversationMessages(session.TranscriptJSON)
	if err != nil {
		return pageData{}, fmt.Errorf("decode session transcript: %w", err)
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
	return pageData{
		Title:      fmt.Sprintf("Session %d", session.ID),
		Session:    session,
		Messages:   messages,
		Events:     eventViews,
		SessionURL: sessionURL(sessionID),
		Pending:    pending.Prompt,
		Failure:    pending.Failure,
	}, nil
}
