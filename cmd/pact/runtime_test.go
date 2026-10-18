package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/nix"
)

func TestRuntimeCancellationStopsContainer(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real Docker checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	label := "me.alexghr.pact.test=" + rand.Text()
	containers := func(ctx context.Context) (string, error) {
		output, err := exec.CommandContext(ctx, "docker", "container", "ls", "--all", "--filter", "label="+label, "--format", "{{.ID}}").CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ids, _ := containers(cleanupCtx)
		if ids != "" {
			_ = exec.CommandContext(cleanupCtx, "docker", append([]string{"rm", "--force"}, strings.Fields(ids)...)...).Run()
		}
	})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan struct{})
	type result struct {
		started bool
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		started, err := runContainer(runCtx, []string{"run", "--rm", "--label", label,
			"--network", "none", "--entrypoint", "/bin/bash", image,
			"-euc", "printf 'ready\\n'; exec sleep infinity"}, func() error { return nil }, func(input io.Reader) error {
			reader := bufio.NewReader(input)
			line, err := reader.ReadString('\n')
			if err != nil || line != "ready\n" {
				return fmt.Errorf("runtime did not become ready: %q, %v", line, err)
			}
			close(ready)
			stop()
			_, err = io.Copy(io.Discard, reader)
			return err
		})
		finished <- result{started, err}
	}()
	select {
	case result := <-finished:
		if !result.started || result.err == nil {
			t.Fatalf("interrupted runtime: started %v, %v", result.started, result.err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled runtime did not release its event stream")
	}
	select {
	case <-ready:
	default:
		t.Fatal("runtime exited before the readiness event")
	}
	if ids, err := containers(ctx); err != nil || ids != "" {
		t.Fatalf("container survived cancellation: %q, %v", ids, err)
	}
}
