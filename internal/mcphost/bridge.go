// Package mcphost serves MCP handlers through a bridge to Pact's container.
package mcphost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/alexghr/pact/mcpproxy"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Bridge owns one run's listener and container connection. Start returns a
// ready bridge; defer Close to revoke access and clean up its resources.
type Bridge interface {
	AgentURL() string
	ContainerConfig() ContainerConfig
	Done() <-chan error
	Close() error
}

type BindMount struct {
	Source string
	Target string
}

type ContainerConfig struct {
	ReadOnlyMounts []BindMount
	Env            []string
}

// Start returns a running bridge for the local Docker host. macOS Docker Desktop
// cannot mount host Unix sockets; local Linux Docker uses a socket and relay.
func Start(servers []mcpproxy.Server, workspace string) (Bridge, error) {
	if runtime.GOOS == "darwin" {
		bridge, err := startTCP(servers)
		if err != nil {
			return nil, err
		}
		return bridge, nil
	}
	bridge, err := startUnix(servers, workspace)
	if err != nil {
		return nil, err
	}
	return bridge, nil
}

// host shares request serving and shutdown between bridge implementations.
type host struct {
	server   *http.Server
	cancel   context.CancelFunc
	done     chan error
	once     sync.Once
	closeErr error
}

func routes(servers []mcpproxy.Server) (http.Handler, error) {
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
	return mux, nil
}

func (h *host) serve(listener net.Listener, handler http.Handler) {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	h.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		err := h.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		h.done <- err
		close(h.done)
	}()
}

// Done reports listener failure or completion after Close.
func (h *host) Done() <-chan error { return h.done }

// close revokes access, cancels active requests, and cleans up once.
func (h *host) close(cleanup func() error) error {
	h.once.Do(func() {
		h.cancel()
		h.closeErr = h.server.Close()
		if cleanup != nil {
			h.closeErr = errors.Join(h.closeErr, cleanup())
		}
	})
	return h.closeErr
}
