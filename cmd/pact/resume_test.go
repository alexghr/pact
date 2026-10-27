package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/codex"
)

func TestLiveResumeContextAndPermissions(t *testing.T) {
	if os.Getenv("PACT_TEST_CODEX_LIVE") != "1" {
		t.Skip("disposable live Codex validation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	home := t.TempDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		codexHome = filepath.Join(userHome, ".codex")
	}
	snapshot, err := codex.CreateAuthSnapshot(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(snapshot, filepath.Join(home, "auth.json")); err != nil {
		os.Remove(snapshot)
		t.Fatal(err)
	}
	workspace := t.TempDir()
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "CODEX_HOME" && key != "CODEX_THREAD_ID" && key != "CODEX_SESSION_ID" && key != "OPENAI_API_KEY" {
			env = append(env, value)
		}
	}
	env = append(env, "CODEX_HOME="+home)
	run := func(approval, sandbox string, args ...string) []byte {
		t.Helper()
		flags := []string{"-c", `model="gpt-6.1-sol"`, "-c", `model_reasoning_effort="low"`,
			"-c", fmt.Sprintf("approval_policy=%q", approval), "-c", fmt.Sprintf("sandbox_mode=%q", sandbox)}
		command := exec.CommandContext(ctx, "codex", append(flags, args...)...)
		command.Env, command.Dir = env, workspace
		var output, diagnostics bytes.Buffer
		command.Stdout, command.Stderr = &output, &diagnostics
		if err := command.Run(); err != nil {
			t.Fatalf("live Codex: %v\n%s\n%s", err, output.String(), diagnostics.String())
		}
		return output.Bytes()
	}
	token := rand.Text()
	first := run("on-request", "read-only", "exec", "--json", "--ignore-user-config", "--skip-git-repo-check", "--",
		"Remember the continuation token "+token+". For this turn only, reply ACK without using tools.")
	var threadID string
	for line := range strings.Lines(string(first)) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "thread.started" {
			threadID = event.ThreadID
		}
	}
	if threadID == "" {
		t.Fatal("no saved native conversation")
	}
	outside := filepath.Join(t.TempDir(), "continued-token")
	continued := run("never", "danger-full-access", "exec", "resume", "--dangerously-bypass-approvals-and-sandbox",
		"--json", "--ignore-user-config", "--skip-git-repo-check", "--", threadID,
		"Use a shell command to write the exact continuation token from our earlier message, without a newline, to "+outside+". Make no other file changes. Then reply DONE.")
	if !bytes.Contains(continued, []byte(threadID)) {
		t.Fatal("continuation changed the native conversation")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != token {
		t.Fatalf("context or full-access write failed: %q, %v\n%s", data, err, continued)
	}
}
