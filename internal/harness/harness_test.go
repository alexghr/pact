package harness

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/state"
)

func TestRunnerRejectsInvalidPolicyOptions(t *testing.T) {
	runner := &Runner{}
	if _, err := runner.Run(context.Background(), 1, Options{Image: "generic"}, nil); err == nil ||
		!strings.Contains(err.Error(), "prompt must not be empty") {
		t.Fatalf("empty prompt error = %v", err)
	}
	if _, err := runner.Run(context.Background(), 1, Options{Prompt: "hello", Image: "../private"}, nil); err == nil ||
		!strings.Contains(err.Error(), "unsupported image") {
		t.Fatalf("unsupported image error = %v", err)
	}
}

func TestCanonicalWorkspace(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	got, err := canonicalWorkspace(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonicalWorkspace() = %q, want %q", got, want)
	}
}

func TestCanonicalWorkspaceRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalWorkspace(path); err == nil {
		t.Fatal("canonicalWorkspace() accepted a file")
	}
}

func TestCanonicalWorkspaceRejectsUnsafeMountPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe:path")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalWorkspace(path); err == nil {
		t.Fatal("canonicalWorkspace() accepted a path containing ':'")
	}
}

func TestValidateResumeTarget(t *testing.T) {
	err := validateResumeTarget(8, "/tmp/project", &state.ResumeTarget{
		PactSessionID: 7,
		WorkspaceDir:  "/tmp/project",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to session 7") {
		t.Fatalf("validateResumeTarget() cross-session error = %v", err)
	}
	err = validateResumeTarget(7, "/tmp/other", &state.ResumeTarget{
		PactSessionID: 7,
		WorkspaceDir:  "/tmp/project",
	})
	if err == nil || !strings.Contains(err.Error(), "belongs to workspace") {
		t.Fatalf("validateResumeTarget() workspace error = %v", err)
	}
}

func TestProcessExitCode(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 23").Run()
	code := processExitCode(err)
	if code == nil || *code != 23 {
		t.Fatalf("processExitCode() = %v, want 23", code)
	}
}

func TestProcessExitCodeUnknown(t *testing.T) {
	if code := processExitCode(errors.New("docker unavailable")); code != nil {
		t.Fatalf("processExitCode() = %d, want nil", *code)
	}
}

func TestWaitForCodexTurnStreamsAgentMessage(t *testing.T) {
	messages := strings.NewReader(
		`{"method":"item/agentMessage/delta","params":{"threadId":"other","turnId":"other","delta":"ignore"}}` + "\n" +
			`{"method":"item/agentMessage/delta","params":{"threadId":"thr_123","turnId":"turn_456","delta":"done"}}` + "\n" +
			`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"other","items":[],"status":"completed"}}}` + "\n" +
			`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"turn_456","items":[],"status":"completed"}}}` + "\n",
	)
	client := codex.NewClient(messages, io.Discard)
	var output bytes.Buffer

	if err := waitForCodexTurn(context.Background(), client, &output, "thr_123", "turn_456", nil); err != nil {
		t.Fatal(err)
	}
	if output.String() != "done" {
		t.Fatalf("agent output = %q, want done", output.String())
	}
}

func TestWaitForCodexTurnReturnsFailure(t *testing.T) {
	messages := strings.NewReader(
		`{"method":"turn/completed","params":{"threadId":"thr_123","turn":{"id":"turn_456","items":[],"status":"failed","error":{"message":"model unavailable"}}}}` + "\n",
	)
	client := codex.NewClient(messages, io.Discard)

	err := waitForCodexTurn(context.Background(), client, io.Discard, "thr_123", "turn_456", nil)
	if err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("waitForCodexTurn() error = %v", err)
	}
}

func TestShouldRecordCodexEvent(t *testing.T) {
	for _, method := range []string{
		"item/completed",
		"turn/completed",
		"thread/tokenUsage/updated",
		"model/rerouted",
	} {
		if !shouldRecordCodexEvent(codex.Message{Method: method}) {
			t.Errorf("shouldRecordCodexEvent(%q) = false", method)
		}
	}
	for _, method := range []string{
		"item/agentMessage/delta",
		"item/commandExecution/outputDelta",
		"turn/diff/updated",
	} {
		if shouldRecordCodexEvent(codex.Message{Method: method}) {
			t.Errorf("shouldRecordCodexEvent(%q) = true", method)
		}
	}
}
