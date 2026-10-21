package codex

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This exercises real cross-client notifications, unsaved-thread exclusion, and
// saved-turn discovery and transport cleanup without an upstream model service.
func TestAppServerObserver(t *testing.T) {
	if os.Getenv("PACT_TEST_CODEX") != "1" {
		t.Skip("set PACT_TEST_CODEX=1 to run the real Codex app-server")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	home := t.TempDir()
	// An unavailable loopback provider accepts the user's turn without
	// credentials, model work, or an HTTP server impersonating a provider.
	config := `model = "offline"
model_provider = "offline"
[model_providers.offline]
name = "Offline"
base_url = "http://127.0.0.1:0/v1"
wire_api = "responses"
requires_openai_auth = false
request_max_retries = 0
stream_max_retries = 0
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	command, observer := startAppServer(t, ctx, home)
	socket := filepath.Join(home, "server.sock")
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	actor, err := Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { actor.Close() })
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	if err := actor.call(ctx, "thread/start", map[string]any{"cwd": home, "approvalPolicy": "on-request", "sandbox": "read-only"}, &started); err != nil {
		t.Fatal(err)
	}
	if started.ApprovalPolicy != "on-request" || started.Sandbox.Type != "readOnly" {
		t.Fatalf("test thread was not restrictive: %+v", started)
	}
	select {
	case <-observer.Changes():
	case <-ctx.Done():
		t.Fatal("observer did not receive thread creation")
	}
	threads, err := observer.Threads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 0 {
		t.Fatalf("unsaved conversation entered the catalogue: %+v", threads)
	}
	if err := actor.call(ctx, "thread/name/set", map[string]any{"threadId": started.Thread.ID, "name": "observer test"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observer.Changes():
	case <-ctx.Done():
		t.Fatal("observer did not receive rename")
	}
	// A notification must not block RPC responses even when Pact has not drained
	// Changes. The second client does not subscribe to or retain these threads.
	if err := actor.call(ctx, "thread/fork", map[string]any{"threadId": started.Thread.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.Threads(ctx); err != nil {
		t.Fatal(err)
	}
	if err := actor.call(ctx, "turn/start", map[string]any{
		"threadId": started.Thread.ID,
		"input":    []map[string]any{{"type": "text", "text": "Saved offline turn", "text_elements": []any{}}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		threads, err = observer.Threads(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, thread := range threads {
			if thread.ID != started.Thread.ID {
				continue
			}
			found = true
			if thread.Title != "observer test" || thread.CreatedAt <= 0 || thread.LastUsed < thread.CreatedAt || thread.Archived {
				t.Fatalf("incorrect saved metadata: %+v", thread)
			}
		}
		if found {
			break
		}
		select {
		case <-observer.Changes():
		case <-ticker.C: // Native persistence may finish after the start notification.
		case <-ctx.Done():
			t.Fatal("accepted user input missing from saved conversations")
		}
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observer.Done():
	case <-ctx.Done():
		t.Fatal("observer did not notice transport loss")
	}
	if observer.Err() == nil {
		t.Fatal("transport loss has no error")
	}
	_ = command.Wait()
	_, prepared := startAppServer(t, ctx, home)
	if err := prepared.PrepareResume(ctx, started.Thread.ID); err != nil {
		t.Fatal(err)
	}
	// A second client performs the ID-only resume used by the remote TUI.
	// Check effective server state, not the arguments sent by Pact.
	tui, err := Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer tui.Close()
	var resumed struct {
		ApprovalPolicy string `json:"approvalPolicy"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	if err := tui.call(ctx, "thread/resume", map[string]any{"threadId": started.Thread.ID, "excludeTurns": true}, &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.ApprovalPolicy != "never" || resumed.Sandbox.Type != "dangerFullAccess" {
		t.Fatalf("remote resume restored restrictive permissions: %+v", resumed)
	}
}

func startAppServer(t *testing.T, ctx context.Context, home string) (*exec.Cmd, *Client) {
	t.Helper()
	binary := os.Getenv("PACT_CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	socket := filepath.Join(home, "server.sock")
	command := exec.CommandContext(ctx, binary, "app-server", "--listen", "unix://"+socket)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key != "CODEX_HOME" && key != "CODEX_THREAD_ID" && key != "CODEX_SESSION_ID" && key != "OPENAI_API_KEY" && strings.ToUpper(key) != "NO_PROXY" {
			command.Env = append(command.Env, item)
		}
	}
	command.Env = append(command.Env, "CODEX_HOME="+home, "NO_PROXY=127.0.0.1")
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command.Process.Kill()
		command.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	})

	var observer *Client
	// Retry the actual handshake until the child is listening, with a deadline.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var err error
		observer, err = Dial(ctx, socket)
		if err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("app-server startup: %v", err)
		}
	}
	t.Cleanup(func() { observer.Close() })
	return command, observer
}
