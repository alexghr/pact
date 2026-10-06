package mcphost

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/alexghr/pact/mcpproxy"
)

type tcpBridge struct {
	host
	hostURL  string
	agentURL string
}

// startTCP creates a loopback bridge for Docker Desktop. Its random URL path
// grants access only to this run's bridge, never to upstream credentials.
func startTCP(servers []mcpproxy.Server) (*tcpBridge, error) {
	mux, err := routes(servers)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on MCP TCP bridge: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	token := rand.Text()
	prefix := "/" + token
	b := &tcpBridge{
		hostURL:  fmt.Sprintf("http://127.0.0.1:%d%s", port, prefix),
		agentURL: fmt.Sprintf("http://host.docker.internal:%d%s", port, prefix),
	}
	stripped := http.StripPrefix(prefix, mux)
	b.serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pathToken, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if subtle.ConstantTimeCompare([]byte(pathToken), []byte(token)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		stripped.ServeHTTP(w, r)
	}))
	return b, nil
}

func (b *tcpBridge) AgentURL() string                 { return b.agentURL }
func (b *tcpBridge) ContainerConfig() ContainerConfig { return ContainerConfig{} }
func (b *tcpBridge) Close() error                     { return b.close(nil) }
