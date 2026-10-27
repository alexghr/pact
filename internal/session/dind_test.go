package session

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
)

func TestDINDStartCancellation(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real DinD cancellation")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// Docker's event history closes the subscription/startup race.
	events := exec.CommandContext(ctx, "docker", "events", "--since", time.Now().UTC().Format(time.RFC3339Nano),
		"--filter", "type=container", "--filter", "event=create", "--filter", "image="+dindImage,
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
	created := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			created <- scanner.Text()
			stop()
		}
	}()
	volume, cleanup, err := startDIND(startCtx, t.TempDir())
	if err == nil {
		defer cleanup()
		t.Fatal("startup was not interrupted at resource creation")
	}
	if startCtx.Err() == nil {
		t.Fatalf("startup failed before cancellation: %v", err)
	}
	var id string
	select {
	case id = <-created:
	case <-ctx.Done():
		t.Fatal("no container creation event")
	}
	// Inspect by listing because the container should already have been removed.
	outputIDs, err := exec.CommandContext(ctx, "docker", "container", "ls", "--all", "--filter", "id="+id, "--format", "{{.ID}}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(outputIDs)) != "" {
		t.Fatalf("cancelled startup leaked container: %s, %v", outputIDs, err)
	}
	if info, err := docker.Inspect(ctx, "volume", volume); err != nil || info != nil {
		t.Fatalf("cancelled startup leaked socket volume: %+v, %v", info, err)
	}
}
