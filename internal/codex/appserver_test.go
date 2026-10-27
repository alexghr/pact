package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// An unavailable provider still persists accepted user turns. Exercise native
// exec resume and the subsequent remote-TUI handshake against that real store.
func TestExecResumeRetainsContext(t *testing.T) {
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
	if err := client.call(ctx, "turn/start", map[string]any{
		"threadId": started.Thread.ID,
		"input":    []map[string]any{{"type": "text", "text": original}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var saved struct {
			Thread struct {
				Turns []json.RawMessage `json:"turns"`
			} `json:"thread"`
		}
		err := client.call(ctx, "thread/read", map[string]any{"threadId": started.Thread.ID, "includeTurns": true}, &saved)
		if err == nil && len(saved.Thread.Turns) != 0 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("initial conversation was not saved")
		}
	}
	_ = server.Process.Kill()
	_ = server.Wait()
	binary := os.Getenv("PACT_CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	execCtx, stopExec := context.WithCancel(ctx)
	defer stopExec()
	command := exec.CommandContext(execCtx, binary,
		"-c", `model="offline"`, "-c", `model_provider="offline"`,
		"-c", `model_providers.offline={name="Offline",base_url="http://127.0.0.1:0/v1",wire_api="responses",requires_openai_auth=false,request_max_retries=0,stream_max_retries=0}`,
		"-c", `approval_policy="never"`, "-c", `sandbox_mode="danger-full-access"`,
		"exec", "resume", "--dangerously-bypass-approvals-and-sandbox", "--json", "--ignore-user-config", "--skip-git-repo-check", "--", started.Thread.ID, continuation)
	command.Env = server.Env
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.WaitDelay = 3 * time.Second
	var output, diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	events, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	found, accepted := false, false
	scanner := bufio.NewScanner(events)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(&output, line)
		var event execEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			break
		}
		if event.Type == "thread.started" {
			if event.ThreadID != started.Thread.ID {
				break
			}
			found = true
		}
		if event.Type == "turn.started" {
			accepted = true
			break
		}
	}
	// Interrupt after the real turn-acceptance event; no provider response is
	// needed to check persistence and continuation into the interactive client.
	stopExec()
	_ = command.Wait()
	if !found || !accepted {
		t.Fatalf("native continuation did not resume: %s\n%s", output.String(), diagnostics.String())
	}
	_, resumed := startAppServer(t, ctx, home)
	// Cold ID-only resume can lose native sandbox provenance. Interactive
	// continuation uses Pact's permission preparation before rejoining.
	if err := resumed.PrepareResume(ctx, started.Thread.ID); err != nil {
		t.Fatal(err)
	}
	var history json.RawMessage
	if err := resumed.call(ctx, "thread/turns/list", map[string]any{
		"threadId": started.Thread.ID, "itemsView": "full", "limit": 100,
	}, &history); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(history, []byte(original)) || !bytes.Contains(history, []byte(continuation)) {
		t.Fatalf("interactive continuation lost saved context: %s", history)
	}
	unavailable := exec.CommandContext(ctx, binary, command.Args[1:]...)
	unavailable.Args[len(unavailable.Args)-2] = "0199a213-81c0-7800-8aa1-bbab2a035a53"
	unavailable.Env = server.Env
	result, err := unavailable.CombinedOutput()
	if err == nil || bytes.Contains(result, []byte(`"type":"thread.started"`)) {
		t.Fatalf("missing target started a replacement conversation: %s, %v", result, err)
	}
}
