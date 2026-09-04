package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestParseRunOptionsFlags(t *testing.T) {
	options, err := parseRunOptions([]string{
		"--dir", "/tmp/project",
		"--model", "codex-model",
		"--effort", "high",
		"--image", "go",
		"--resume", "42",
		"--migrate",
		"write tests",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if options.Workspace != "/tmp/project" || options.Prompt != "write tests" ||
		options.Model != "codex-model" || options.Effort != "high" || options.Image != "go" ||
		options.resumeSession != 42 || !options.migrate {
		t.Fatalf("parseRunOptions() = %#v", options)
	}
}

func TestParseDatabaseOptions(t *testing.T) {
	migrate, err := parseDatabaseOptions("web", []string{"--migrate"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !migrate {
		t.Fatal("parseDatabaseOptions() did not enable migrations")
	}
	if _, err := parseDatabaseOptions("web", []string{"unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("parseDatabaseOptions() accepted a positional argument")
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

func TestParseRunOptionsFromUsesStoredDefaults(t *testing.T) {
	defaults := defaultRunOptions()
	defaults.Model = "original-model"
	defaults.Effort = "high"
	defaults.Image = "go"
	defaults.resumeSession = 7

	options, err := parseRunOptionsFrom([]string{"--resume", "7", "continue"}, &bytes.Buffer{}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if options.Model != "original-model" || options.Effort != "high" || options.Image != "go" {
		t.Fatalf("parseRunOptionsFrom() = %#v", options)
	}
}

func TestParseRunOptionsFromOverwritesStoredDefaults(t *testing.T) {
	defaults := defaultRunOptions()
	defaults.Model = "original-model"
	defaults.Effort = "high"
	defaults.Image = "go"
	defaults.resumeSession = 7

	options, err := parseRunOptionsFrom([]string{
		"--resume", "7",
		"--model", "new-model",
		"--effort", "medium",
		"--image", "generic",
		"continue",
	}, &bytes.Buffer{}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if options.Model != "new-model" || options.Effort != "medium" || options.Image != "generic" {
		t.Fatalf("parseRunOptionsFrom() = %#v", options)
	}
}

func TestWriteRunList(t *testing.T) {
	var output bytes.Buffer
	err := writeRunList(&output, []state.RunRecord{{
		ID:                7,
		PactSessionID:     3,
		Status:            "running",
		WorkspaceDir:      "/tmp/project",
		Model:             "gpt-5.6-sol",
		Effort:            "low",
		DockerfileVariant: "go",
	}})
	if err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, value := range []string{"ID", "SESSION", "STATUS", "7", "3", "running", "/tmp/project", "gpt-5.6-sol", "low", "go"} {
		if !strings.Contains(got, value) {
			t.Fatalf("run list %q does not contain %q", got, value)
		}
	}
}
