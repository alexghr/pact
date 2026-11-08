package approval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/unixhttp"
)

func TestTerminalDecisions(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	input, keyboard, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyboard.Close() })
	display, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = display.Close(); _ = output.Close() })
	// Pipe deadlines also bound a failed prompt or shutdown assertion.
	deadline, _ := ctx.Deadline()
	_ = display.SetReadDeadline(deadline)
	_ = keyboard.SetWriteDeadline(deadline)
	socket := filepath.Join(t.TempDir(), "approval.sock")
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, socket, input, output) }()
	t.Cleanup(cancel)
	reader := bufio.NewReader(display)
	readThrough := func(want string) string {
		t.Helper()
		var text strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("waiting for %q, read %q: %v", want, text.String(), err)
			}
			text.WriteString(line)
			if strings.Contains(line, want) {
				return text.String()
			}
		}
	}
	readThrough("Listening for approvals")
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("approval socket is not private: %v, %v", info, err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(config.Approvals{Socket: socket}, Session{
		ID: 42, Key: "session-key", Workspace: t.TempDir(), Title: "Fix deployment",
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		allowed bool
		err     error
	}
	invoke := func(ctx context.Context, tool string) <-chan result {
		resultCh := make(chan result, 1)
		go func() {
			allowed, err := client.Approve(ctx, ToolCall{
				Server: "forgejo", Tool: tool, Arguments: jsontext.Value(`{"title":"Investigate deployment"}`),
			})
			resultCh <- result{allowed, err}
		}()
		return resultCh
	}
	await := func(results <-chan result) result {
		t.Helper()
		select {
		case r := <-results:
			return r
		case <-ctx.Done():
			t.Fatal("approval did not finish")
			return result{}
		}
	}
	answer := func(text string) {
		t.Helper()
		if _, err := fmt.Fprintln(keyboard, text); err != nil {
			t.Fatal(err)
		}
	}

	request := invoke(ctx, "create_issue")
	shown := readThrough("Allow? [y/n]")
	for _, want := range []string{hostname, "session-key", "Fix deployment", "forgejo", "create_issue", "Investigate deployment"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("prompt missing %q: %s", want, shown)
		}
	}
	answer("maybe")
	readThrough("Allow? [y/n]")
	answer(" y ")
	if r := await(request); !r.allowed || r.err != nil {
		t.Fatalf("y did not approve: %+v", r)
	}

	// Keep the HTTP caller connected beyond expiresAt: the server must cancel
	// its own input read, discard a partial answer, and advance a queued call.
	data, err := json.Marshal(Request{ID: "expired-call", ExpiresAt: time.Now().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://pact/v1/approvals", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: unixhttp.Dialer(socket)}
	defer transport.CloseIdleConnections()
	expired := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Transport: transport}).Do(req)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusRequestTimeout {
				err = fmt.Errorf("expired request returned HTTP %d", response.StatusCode)
			}
		}
		expired <- err
	}()
	readThrough("Allow? [y/n]")
	if _, err := fmt.Fprint(keyboard, "y"); err != nil {
		t.Fatal(err)
	}
	next := invoke(ctx, "delete_issue")
	readThrough("Request cancelled or expired")
	if shown := readThrough("Allow? [y/n]"); !strings.Contains(shown, "delete_issue") {
		t.Fatalf("queued call did not get its own prompt: %s", shown)
	}
	select {
	case err := <-expired:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("expired request did not finish")
	}
	answer("n")
	if r := await(next); r.allowed || r.err != nil {
		t.Fatalf("n did not reject: %+v", r)
	}

	callCtx, stopCall := context.WithCancel(ctx)
	defer stopCall()
	request = invoke(callCtx, "cancelled_call")
	readThrough("Allow? [y/n]")
	stopCall()
	if r := await(request); r.allowed || r.err == nil {
		t.Fatalf("cancelled call was allowed: %+v", r)
	}
	readThrough("Request cancelled or expired")

	request = invoke(ctx, "eof_call")
	readThrough("Allow? [y/n]")
	_ = keyboard.Close()
	if r := await(request); r.allowed || r.err == nil {
		t.Fatalf("EOF authorized a call: %+v", r)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("approval service did not stop on EOF")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("approval socket survived shutdown: %v", err)
	}
}
