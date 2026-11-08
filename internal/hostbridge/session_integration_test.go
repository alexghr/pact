package hostbridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/images"
	"github.com/alexghr/pact/internal/mcpproxy"
)

func TestRelayStreamingAndSessionLifetime(t *testing.T) {
	ended := make(chan struct{}, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		ended <- struct{}{}
	}))
	defer upstream.Close()
	proxy, err := mcpproxy.New(upstream.URL+"/mcp", nil, mcpproxy.Policy{AllowedTools: []string{"^stream$"}})
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := startUnix([]Server{{Path: "/mcp/example", Handler: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	socketPath := bridge.socketPath
	for path, mode := range map[string]os.FileMode{socketPath: 0600, filepath.Dir(socketPath): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private socket permissions for %s: %v, %v", path, info, err)
		}
	}
	endpoint, _ := startRelay(t, socketPath)
	endpoint += "/mcp/example"
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	request := func(ctx context.Context, method string) *http.Response {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"stream"}}`)
		}
		r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res
	}
	readEvent := func(res *http.Response) {
		t.Helper()
		line, err := bufio.NewReader(res.Body).ReadString('\n')
		if err != nil || line != "data: ready\n" {
			t.Fatalf("event was buffered or changed: %q, %v", line, err)
		}
	}
	waitForCancellation := func() {
		t.Helper()
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("upstream stream was not cancelled")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := request(ctx, http.MethodPost)
	readEvent(res)
	// A long-lived stream must not prevent another MCP request from completing.
	deleted := request(context.Background(), http.MethodDelete)
	if deleted.StatusCode != http.StatusNoContent {
		t.Fatalf("concurrent session termination: %s", deleted.Status)
	}
	deleted.Body.Close()
	cancel()
	res.Body.Close()
	waitForCancellation()

	res = request(context.Background(), http.MethodGet)
	readEvent(res)
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	waitForCancellation()
	if _, err := os.Stat(filepath.Dir(socketPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket directory survived shutdown: %v", err)
	}
	if _, err := client.Get(endpoint); err == nil {
		t.Fatal("proxy still accepted requests after shutdown")
	}
	select {
	case err := <-bridge.Done():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket server did not stop")
	}
}

func startRelay(t *testing.T, socket string) (string, string) {
	t.Helper()
	// Exercise the same relay script embedded in the container image. p-test
	// supplies socat and curl; missing dependencies must fail this check.
	script, err := images.GetAgentFile("env/host-relay.sh")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, "bash", "-e", "-s", "--", socket, fmt.Sprint(port))
	cmd.Stdin = strings.NewReader(script + "\nstart_host_relay \"$1\" \"$2\"\necho ready\nwait \"$pact_host_relay_pid\"\n")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	diagnostics, err := os.CreateTemp(t.TempDir(), "stderr-")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { diagnostics.Close() })
	cmd.Stderr = diagnostics
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("relay did not become ready: %q, %v", line, err)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port), diagnostics.Name()
}

func TestRelayResetDiagnostics(t *testing.T) {
	ended := make(chan struct{})
	bridge, err := startUnix([]Server{{Path: "/harness/stream", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(ended)
		data := make([]byte, 64*1024)
		for {
			if _, err := w.Write(data); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	endpoint, diagnostics := startRelay(t, bridge.socketPath)
	conn, err := net.Dial("tcp4", strings.TrimPrefix(endpoint, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET /harness/stream HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	// Reset a live stream, reproducing the socat error seen over the TUI.
	if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("reset did not end the upstream stream")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	res, err := client.Get(endpoint + "/_pact/health")
	if err != nil {
		t.Fatalf("relay stopped serving after reset: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("relay health after reset: %s", res.Status)
	}
	output, err := os.ReadFile(diagnostics)
	if err != nil || len(output) != 0 {
		t.Fatalf("relay diagnostics leaked to terminal: %s, %v", output, err)
	}
}

func TestRelayStartupDiagnostics(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	script, err := images.GetAgentFile("env/host-relay.sh")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-e", "-s", "--", socket, fmt.Sprint(occupied.Addr().(*net.TCPAddr).Port))
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	cmd.Stdin = strings.NewReader(script + "\nstart_host_relay \"$1\" \"$2\"\n")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), occupied.Addr().String()) {
		t.Fatalf("startup failure lost diagnostics: %s, %v", output, err)
	}
}
