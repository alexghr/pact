package hostbridge

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

type unixBridge struct {
	host
	dir        string
	socketPath string
}

// startUnix creates a private socket bridge for a local Linux Docker daemon,
// rejecting a socket directory exposed by the workspace mount.
func startUnix(servers []Server, workspace string) (*unixBridge, error) {
	mux, err := routes(servers)
	if err != nil {
		return nil, err
	}
	// Use a short, host-local path independent of TMPDIR.
	dir, err := os.MkdirTemp("/tmp", "pact-mcp-")
	if err != nil {
		return nil, fmt.Errorf("create host bridge socket directory: %w", err)
	}
	// Failed startup must not leave a socket or directory behind.
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(dir)
		}
	}()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve host bridge socket directory: %w", err)
	}
	dir = resolvedDir
	relativeSocket, err := filepath.Rel(workspace, filepath.Join(dir, "mcp.sock"))
	if err != nil {
		return nil, err
	}
	if filepath.IsLocal(relativeSocket) {
		return nil, errors.New("host bridge socket directory must be outside the mounted workspace; choose a narrower workspace")
	}
	socketPath := filepath.Join(dir, "mcp.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on host bridge socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("restrict host bridge socket access: %w", err)
	}
	b := &unixBridge{dir: dir, socketPath: socketPath}
	b.serve(listener, mux)
	started = true
	return b, nil
}

func (b *unixBridge) AgentURL() string { return "http://127.0.0.1:8765" }

func (b *unixBridge) ContainerConfig() ContainerConfig {
	// Preserve the socket path and environment variable used by existing images.
	return ContainerConfig{
		ReadOnlyMounts: []BindMount{{Source: b.socketPath, Target: "/opt/pact/mcp.sock"}},
		Env:            []string{"PACT_MCP_SOCKET=/opt/pact/mcp.sock"},
	}
}

func (b *unixBridge) Close() error {
	return b.close(func() error {
		return os.RemoveAll(b.dir)
	})
}
