// Package daemon owns live sessions and their terminal attachments.
package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/session"
	"github.com/alexghr/pact/internal/state"
)

type request struct {
	Options    *session.Options    `json:"options,omitempty"`
	SessionID  int64               `json:"sessionId,omitempty"`
	ResumeID   *int64              `json:"resumeId,omitempty"`
	ConfigPath string              `json:"configPath,omitempty"`
	Detach     bool                `json:"detach,omitempty"`
	Data       []byte              `json:"data,omitempty"`
	Size       docker.TerminalSize `json:"size"`
}

type response struct {
	SessionID int64  `json:"sessionId,omitempty"`
	Data      []byte `json:"data,omitempty"`
	Done      bool   `json:"done,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Serve owns all live sessions until cancellation.
func Serve(ctx context.Context, cfg config.HostConfig) error {
	socket, err := socketPath(true)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	store, err := session.OpenStore(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var mu sync.Mutex
	sessions := make(map[int64]*liveSession)
	fmt.Fprintf(os.Stderr, "Pact daemon listening on %s\n", socket)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		workers.Go(func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
			var req request
			if err := decoder.Decode(&req); err != nil {
				return
			}
			if req.ResumeID != nil && req.Options != nil {
				_ = encoder.Encode(response{Error: "cannot start and resume a session together"})
				return
			}
			configPath := req.ConfigPath
			if req.Options != nil {
				configPath = req.Options.ConfigPath
			}
			if req.Options != nil || req.ResumeID != nil {
				if err := validateSessionConfig(cfg, configPath); err != nil {
					_ = encoder.Encode(response{Error: err.Error()})
					return
				}
			}
			// Serialize provisioning; the actual executions run independently.
			mu.Lock()
			var err error
			current := sessions[req.SessionID]
			if req.ResumeID != nil {
				req.SessionID, current, err = resumeTarget(ctx, store, sessions, *req.ResumeID)
			}
			restore := req.ResumeID != nil && current == nil
			if err == nil && (req.Options != nil || restore) {
				var nix session.Nix
				nix, err = session.PrepareNix(ctx)
				if err == nil {
					var run *session.Session
					if restore {
						run, err = session.Restore(ctx, store, nix, req.SessionID, req.ConfigPath)
					} else {
						run, err = session.New(ctx, store, nix, cfg.WorkspaceRoot, *req.Options)
					}
					if err == nil {
						current = newLiveSession()
						req.SessionID = run.Record.ID
						sessions[req.SessionID] = current
						workers.Go(func() { current.run(ctx, run, req.Size) })
					}
				}
			}
			mu.Unlock()
			if err == nil && current == nil {
				err = fmt.Errorf("session %d is not owned by this daemon", req.SessionID)
			}
			if err != nil {
				_ = encoder.Encode(response{Error: err.Error()})
				return
			}
			if err := encoder.Encode(response{SessionID: req.SessionID}); err != nil || req.Detach {
				return
			}
			if err := current.attach(ctx, decoder, encoder, req.Size); err != nil {
				_ = encoder.Encode(response{Error: err.Error()})
			}
		})
	}
}

func validateSessionConfig(daemon config.HostConfig, path string) error {
	requested, err := config.Load(path)
	if err != nil {
		return err
	}
	if requested.DatabasePath != daemon.DatabasePath || requested.WorkspaceRoot != daemon.WorkspaceRoot {
		return fmt.Errorf("daemon storage differs from %s; restart it with pact --config %q daemon", path, path)
	}
	return nil
}

// The caller serializes target selection with provisioning so concurrent resume
// requests reuse the same live run instead of restoring duplicate executions.
func resumeTarget(ctx context.Context, store *state.Store, sessions map[int64]*liveSession, id int64) (int64, *liveSession, error) {
	var saved state.Session
	var err error
	if id == 0 {
		saved, err = store.LastSession(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			err = errors.New("no sessions to resume")
		}
	} else {
		saved, err = store.Session(ctx, id)
	}
	if err != nil {
		return 0, nil, err
	}
	current := sessions[saved.ID]
	if current != nil {
		select {
		case <-current.done:
			current = nil
		default:
		}
	}
	return saved.ID, current, nil
}

type liveSession struct {
	mu        sync.Mutex
	input     chan []byte
	output    []byte
	client    chan []byte
	done      chan struct{}
	err       error
	container string
	size      docker.TerminalSize
}

func newLiveSession() *liveSession {
	return &liveSession{input: make(chan []byte, 64), done: make(chan struct{})}
}

func (s *liveSession) run(ctx context.Context, run *session.Session, size docker.TerminalSize) {
	input, writer := io.Pipe()
	inputCtx, stopInput := context.WithCancel(ctx)
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		s.copyInput(inputCtx, writer)
	}()
	s.mu.Lock()
	s.size = size
	s.mu.Unlock()
	err := run.StartTerminal(ctx, session.Terminal{
		Stdin: input, Stdout: s, Stderr: s, Size: size,
		Ready: func(id string) {
			s.mu.Lock()
			s.container = id
			size := s.size
			s.mu.Unlock()
			_ = docker.ResizeTerminal(ctx, id, size)
		},
	})
	_ = input.Close()
	stopInput()
	<-inputDone
	_ = writer.Close()
	err = errors.Join(err, run.Cleanup())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Pact session %d ended with error: %v\n", run.Record.ID, err)
	}
	s.mu.Lock()
	s.err = err
	close(s.done)
	s.mu.Unlock()
}

// Input delivery belongs to the session, not the connection reader. A stalled
// native stdin must not prevent the reader from noticing client disconnection.
func (s *liveSession) copyInput(ctx context.Context, writer *io.PipeWriter) {
	for {
		select {
		case data := <-s.input:
			if _, err := writer.Write(data); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *liveSession) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = append(s.output, data...)
	// Retain the last 1 MiB of terminal bytes, not a complete screen snapshot.
	// Earlier screen content and setup sequences may be lost. Evaluate the
	// limit with long sessions and resizing: https://forge.alexghr.me/ag/pact/issues/133.
	const historyLimit = 1 << 20
	if len(s.output) > historyLimit {
		s.output = append([]byte(nil), s.output[len(s.output)-historyLimit:]...)
	}
	if s.client != nil {
		select {
		case s.client <- append([]byte(nil), data...):
		default:
			close(s.client)
			s.client = nil
		}
	}
	return len(data), nil
}

func (s *liveSession) attach(ctx context.Context, decoder *json.Decoder, encoder *json.Encoder, size docker.TerminalSize) error {
	s.mu.Lock()
	if s.client != nil {
		s.mu.Unlock()
		return errors.New("session already has an attached terminal")
	}
	updates := make(chan []byte, 64)
	s.client = updates
	history := append([]byte(nil), s.output...)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.client == updates {
			s.client = nil
		}
		s.mu.Unlock()
	}()
	if err := encoder.Encode(response{Data: history}); err != nil {
		return err
	}
	s.resize(ctx, size)
	disconnected := make(chan error, 1)
	go func() {
		for {
			var req request
			if err := decoder.Decode(&req); err != nil {
				disconnected <- err
				return
			}
			if req.Size.Rows > 0 && req.Size.Cols > 0 {
				s.resize(ctx, req.Size)
			}
			if len(req.Data) > 0 {
				select {
				case s.input <- req.Data:
				default:
					disconnected <- errors.New("terminal input overflow; reattach to continue")
					return
				}
			}
		}
	}()
	for {
		select {
		case data, ok := <-updates:
			if !ok {
				return errors.New("terminal output overflow; reattach to replay output")
			}
			if err := encoder.Encode(response{Data: data}); err != nil {
				return err
			}
		case <-s.done:
			// Drain output already queued before reporting the final outcome.
			for len(updates) > 0 {
				if err := encoder.Encode(response{Data: <-updates}); err != nil {
					return err
				}
			}
			result := response{Done: true}
			if s.err != nil {
				result.Error = s.err.Error()
			}
			return encoder.Encode(result)
		case <-ctx.Done():
			return nil
		case err := <-disconnected:
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

func (s *liveSession) resize(ctx context.Context, size docker.TerminalSize) {
	s.mu.Lock()
	s.size = size
	id := s.container
	s.mu.Unlock()
	if id != "" {
		_ = docker.ResizeTerminal(ctx, id, size)
	}
}
