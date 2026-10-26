package sidecar

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/execution"
	"github.com/alexghr/pact/internal/state"
)

func TestDINDStartCancellation(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real DinD cancellation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := state.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	launch, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	owner := &execution.Owner{Store: store, Launch: launch}
	defer func() {
		if err := owner.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	// Docker's event history closes the subscription/startup race. Cancel at
	// real container creation, including the pre-bookkeeping crash window.
	events := exec.CommandContext(ctx, "docker", "events", "--since", time.Now().UTC().Format(time.RFC3339Nano),
		"--filter", "type=container", "--filter", "event=create",
		"--filter", "label=me.alexghr.pact.environment="+launch.Environment.ID,
		"--format", "{{.Actor.ID}}")
	events.Stderr = os.Stderr
	output, err := events.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { events.Process.Kill(); events.Wait() }()
	startCtx, stop := context.WithCancel(ctx)
	defer stop()
	notified := make(chan struct{})
	go func() {
		defer close(notified)
		if bufio.NewScanner(output).Scan() {
			stop()
		}
	}()
	_, err = StartDINDSidecar(startCtx, owner, launch.Workspace)
	if err == nil || startCtx.Err() == nil {
		t.Fatalf("startup was not interrupted at resource creation: %v", err)
	}
	if err := owner.Cleanup(); err != nil {
		t.Fatal(err)
	}
	_ = events.Process.Kill()
	<-notified
	resources, err := store.Resources(ctx, launch.ExecutionID)
	if err != nil || len(resources) != 0 {
		t.Fatalf("cancelled startup leaked resources: %+v, %v", resources, err)
	}
}
