// Package mcphost serves a Pact run's MCP handlers over a private Unix socket.
package mcphost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/alexghr/pact/mcpproxy"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Session owns a listener and its temporary directory. Only SocketPath should
// be mounted into the container, with read-only access and the host's UID.
type Session struct {
	SocketPath string
	dir        string
	server     *http.Server
	cancel     context.CancelFunc
	done       chan error
	once       sync.Once
	closeErr   error
}

// Start validates the complete registration before creating any runtime state.
func Start(servers []mcpproxy.Server) (*Session, error) {
	mux := http.NewServeMux()
	seen := make(map[string]bool)
	for _, server := range servers {
		if !validName.MatchString(server.Name) {
			return nil, fmt.Errorf("invalid MCP server name %q: use letters, digits, underscores, or hyphens", server.Name)
		}
		if seen[server.Name] || server.Handler == nil {
			return nil, fmt.Errorf("MCP server %q has a duplicate name or nil handler", server.Name)
		}
		seen[server.Name] = true
		mux.Handle("/mcp/"+server.Name, server.Handler)
	}
	mux.HandleFunc("GET /_pact/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// Use a short, host-local path independent of TMPDIR. The launcher checks
	// that the workspace mount cannot expose this directory or sibling sessions.
	dir, err := os.MkdirTemp("/tmp", "pact-mcp-")
	if err != nil {
		return nil, fmt.Errorf("create MCP socket directory: %w", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("resolve MCP socket directory: %w", err)
	}
	dir = resolvedDir
	socketPath := filepath.Join(dir, "mcp.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("listen on MCP socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("restrict MCP socket access: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		SocketPath: socketPath,
		dir:        dir,
		cancel:     cancel,
		done:       make(chan error, 1),
		server: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return ctx },
		},
	}
	go func() {
		err := session.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		session.done <- err
		close(session.done)
	}()
	return session, nil
}

// Done reports when serving stops, with a nil error after Close.
func (s *Session) Done() <-chan error { return s.done }

// Close revokes access immediately, including active streams, and removes the
// socket directory. It is safe to call more than once.
func (s *Session) Close() error {
	s.once.Do(func() {
		s.cancel()
		serverErr := s.server.Close()
		directoryErr := os.RemoveAll(s.dir)
		s.closeErr = errors.Join(serverErr, directoryErr)
	})
	return s.closeErr
}
