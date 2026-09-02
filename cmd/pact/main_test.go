package main

import (
	"errors"
	"os/exec"
	"testing"
)

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
