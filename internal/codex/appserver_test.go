package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The real server broadcasts status changes across clients without subscribing
// Pact to transcripts. Bind once, after user input; ignore subsequent navigation.
func TestAppServerBinding(t *testing.T) {
	if os.Getenv("PACT_TEST_CODEX") != "1" {
		t.Skip("set PACT_TEST_CODEX=1 for the real Codex app-server")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	home := offlineCodexHome(t)
	_, observer := startAppServer(t, ctx, home)
	actor, err := Dial(ctx, filepath.Join(home, "server.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer actor.Close()
	start := func() string {
		t.Helper()
		var started struct {
			Thread struct{ ID string } `json:"thread"`
		}
		if err := actor.call(ctx, "thread/start", map[string]any{"cwd": home, "approvalPolicy": "never", "sandbox": "danger-full-access"}, &started); err != nil {
			t.Fatal(err)
		}
		return started.Thread.ID
	}
	first := start()
	select {
	case ref := <-observer.Bindings():
		t.Fatalf("empty TUI thread became a resume target: %s", ref)
	default:
	}
	turn := func(ref string) {
		t.Helper()
		if err := actor.call(ctx, "turn/start", map[string]any{"threadId": ref, "input": []map[string]any{{"type": "text", "text": "Saved offline turn"}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	turn(first)
	select {
	case ref := <-observer.Bindings():
		if ref != first {
			t.Fatalf("binding = %q, want %q", ref, first)
		}
	case <-ctx.Done():
		t.Fatal("initial user turn did not expose a backend reference")
	}
	second := start()
	turn(second)
	if err := actor.call(ctx, "thread/read", map[string]any{"threadId": second}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ref := <-observer.Bindings():
		t.Fatalf("native navigation emitted another binding: %s", ref)
	default:
	}
}

func startAppServer(t *testing.T, ctx context.Context, home string) (*exec.Cmd, *Client) {
	t.Helper()
	binary := os.Getenv("PACT_CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	socket := filepath.Join(home, "server.sock")
	command := exec.CommandContext(ctx, binary, "-c", `sandbox_mode="danger-full-access"`, "-c", `approval_policy="never"`, "app-server", "--listen", "unix://"+socket)
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

func offlineCodexHome(t *testing.T) string {
	t.Helper()
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
	return home
}

// The real client's version command exits without a terminal. Server logs must
// stay off stderr on success, while startup failures must retain diagnostics.
func TestInteractiveDiagnostics(t *testing.T) {
	if os.Getenv("PACT_TEST_CODEX") != "1" {
		t.Skip("set PACT_TEST_CODEX=1 for the real Codex app-server")
	}
	for _, test := range []struct {
		name         string
		serverConfig []string
		wantLog      string
	}{
		{name: "successful exit"},
		{name: "startup failure", serverConfig: []string{"-c", "model=42"}, wantLog: "expected a string"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			t.Setenv("CODEX_HOME", offlineCodexHome(t))
			t.Setenv("NO_PROXY", "127.0.0.1")
			t.Setenv("RUST_LOG", "codex_app_server=trace")
			// Native startup warnings belong to the client and may reach stderr.
			var nativeLogs bytes.Buffer
			command := exec.CommandContext(ctx, "codex", "--version")
			command.Stderr = &nativeLogs
			if err := command.Run(); err != nil {
				t.Fatal(err)
			}
			diagnostics, err := os.CreateTemp(t.TempDir(), "stderr-")
			if err != nil {
				t.Fatal(err)
			}
			defer diagnostics.Close()
			stderr := os.Stderr
			os.Stderr = diagnostics
			defer func() { os.Stderr = stderr }()

			err = runInteractive(ctx, test.serverConfig, []string{"--version"}, "", func(context.Context, string) error {
				t.Error("version command unexpectedly started a conversation")
				return nil
			})
			if (err != nil) != (test.wantLog != "") {
				t.Fatalf("interactive result: %v", err)
			}
			logs, err := os.ReadFile(diagnostics.Name())
			if err != nil {
				t.Fatal(err)
			}
			if test.wantLog == "" && !bytes.Equal(logs, nativeLogs.Bytes()) {
				t.Fatalf("server output leaked to the terminal: %s", logs)
			}
			if !strings.Contains(string(logs), test.wantLog) {
				t.Fatalf("startup diagnostics missing %q: %s", test.wantLog, logs)
			}
		})
	}
}

// An unavailable provider still persists accepted user turns. A cold remote-TUI
// resume must retain that context and establish Pact's container permissions.
func TestInteractiveResumeRetainsContext(t *testing.T) {
	if os.Getenv("PACT_TEST_CODEX") != "1" {
		t.Skip("set PACT_TEST_CODEX=1 to run real Codex continuation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	home := offlineCodexHome(t)
	server, client := startAppServer(t, ctx, home)
	var started struct {
		Thread struct{ ID string } `json:"thread"`
	}
	if err := client.call(ctx, "thread/start", map[string]any{"cwd": home, "approvalPolicy": "on-request", "sandbox": "read-only"}, &started); err != nil {
		t.Fatal(err)
	}
	const original = "The project codename is cobalt-otter."
	const continuation = "Continue using the project codename from our conversation."
	turn := func(client *Client, text string) {
		t.Helper()
		if err := client.call(ctx, "turn/start", map[string]any{
			"threadId": started.Thread.ID,
			"input":    []map[string]any{{"type": "text", "text": text}},
		}, nil); err != nil {
			t.Fatal(err)
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	waitSaved := func(client *Client, text string) json.RawMessage {
		t.Helper()
		for {
			var saved json.RawMessage
			err := client.call(ctx, "thread/read", map[string]any{"threadId": started.Thread.ID, "includeTurns": true}, &saved)
			if err == nil && bytes.Contains(saved, []byte(text)) {
				return saved
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatalf("conversation did not retain %q", text)
			}
		}
	}
	turn(client, original)
	waitSaved(client, original)
	_ = server.Process.Kill()
	_ = server.Wait()
	_, resumed := startAppServer(t, ctx, home)
	if err := resumed.PrepareResume(ctx, started.Thread.ID); err != nil {
		t.Fatal(err)
	}
	waitSaved(resumed, original)
	turn(resumed, continuation)
	if history := waitSaved(resumed, continuation); !bytes.Contains(history, []byte(original)) {
		t.Fatalf("interactive continuation lost saved context: %s", history)
	}
	if err := resumed.PrepareResume(ctx, "0199a213-81c0-7800-8aa1-bbab2a035a53"); err == nil {
		t.Fatal("missing target started a replacement conversation")
	}
}
