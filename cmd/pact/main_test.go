package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/state"
)

func TestParseRunOptionsDefaults(t *testing.T) {
	options, err := parseRunOptions([]string{"fix the tests"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if options.workspace != "." || options.prompt != "fix the tests" ||
		options.model != "gpt-5.6-sol" || options.effort != "low" || options.image != "generic" {
		t.Fatalf("parseRunOptions() = %#v", options)
	}
}

func TestParseRunOptionsFlags(t *testing.T) {
	options, err := parseRunOptions([]string{
		"--dir", "/tmp/project",
		"--model", "codex-model",
		"--effort", "high",
		"--image", "go",
		"write tests",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if options.workspace != "/tmp/project" || options.prompt != "write tests" ||
		options.model != "codex-model" || options.effort != "high" || options.image != "go" {
		t.Fatalf("parseRunOptions() = %#v", options)
	}
}

func TestParseRunOptionsRejectsUnsupportedImage(t *testing.T) {
	_, err := parseRunOptions([]string{"--image", "../private", "prompt"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unsupported image") {
		t.Fatalf("parseRunOptions() error = %v", err)
	}
}

func TestParseRunOptionsRequiresOnePrompt(t *testing.T) {
	for _, args := range [][]string{nil, {"one", "two"}} {
		if _, err := parseRunOptions(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseRunOptions(%q) succeeded", args)
		}
	}
}

func TestParseRunOptionsHelp(t *testing.T) {
	var output bytes.Buffer
	_, err := parseRunOptions([]string{"--help"}, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseRunOptions() error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(output.String(), "Usage: pact run") {
		t.Fatalf("help output = %q", output.String())
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

func TestWriteRunList(t *testing.T) {
	var output bytes.Buffer
	err := writeRunList(&output, []state.RunRecord{{
		ID:                7,
		Status:            "running",
		WorkspaceDir:      "/tmp/project",
		Model:             "gpt-5.6-sol",
		Effort:            "low",
		DockerfileVariant: "go",
		LastAgentMessage:  "first line\nsecond line",
	}})
	if err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, value := range []string{"ID", "STATUS", "LAST TURN", "7", "running", "/tmp/project", "gpt-5.6-sol", "low", "go", "first line second line"} {
		if !strings.Contains(got, value) {
			t.Fatalf("run list %q does not contain %q", got, value)
		}
	}
}

func TestListPreviewTruncatesUnicodeCharacters(t *testing.T) {
	got := listPreview("a\n" + strings.Repeat("界", 50))
	want := "a " + strings.Repeat("界", 48)
	if got != want {
		t.Fatalf("listPreview() = %q, want %q", got, want)
	}
}

func TestListPreviewRemovesTerminalControls(t *testing.T) {
	if got, want := listPreview("\x1b[31mhello\nworld"), "[31mhello world"; got != want {
		t.Fatalf("listPreview() = %q, want %q", got, want)
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
