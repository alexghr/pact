package mcphost_test

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

	"github.com/alexghr/pact/internal/mcphost"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/mcpproxy"
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
	proxy, err := mcpproxy.NewHTTP(upstream.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := mcphost.Start([]mcpproxy.Server{{Name: "example", Handler: proxy}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for path, mode := range map[string]os.FileMode{session.SocketPath: 0600, filepath.Dir(session.SocketPath): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private socket permissions for %s: %v, %v", path, info, err)
		}
	}
	endpoint := startRelay(t, session.SocketPath) + "/mcp/example"
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	request := func(ctx context.Context, method string) *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
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
	res := request(ctx, http.MethodGet)
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
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	waitForCancellation()
	if _, err := os.Stat(filepath.Dir(session.SocketPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket directory survived shutdown: %v", err)
	}
	if _, err := client.Get(endpoint); err == nil {
		t.Fatal("proxy still accepted requests after shutdown")
	}
	select {
	case err := <-session.Done():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket server did not stop")
	}
}

func startRelay(t *testing.T, socket string) string {
	t.Helper()
	// Exercise the same relay script embedded in the container image. p-test
	// supplies socat and curl; missing dependencies must fail this check.
	script, err := container.BuildContext.ReadFile("mcp-relay.sh")
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
	cmd.Stdin = strings.NewReader(string(script) + "\nstart_mcp_relay \"$1\" \"$2\"\necho ready\nwait \"$pact_mcp_relay_pid\"\n")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	cmd.Stderr = os.Stderr
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
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}
