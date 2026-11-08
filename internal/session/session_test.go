package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/images"
	"github.com/alexghr/pact/internal/state"
)

func TestManagedLaunchFailureCanResume(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A real unreachable endpoint fails startup before any agent can run.
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	store, err := OpenStore(t.Context(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "pact", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nix := Nix{Container: "pact-test-nix", Volume: "pact-test-nix-volume"}
	if _, err := New(t.Context(), store, nix, t.TempDir(), Options{Title: "Investigate deployment errors", Model: "saved", Capabilities: state.Capabilities{DIND: true, HostNetwork: true}}); err == nil {
		t.Fatal("launch succeeded without Docker")
	}
	sessions, err := store.Sessions(t.Context(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "startupFailed" || !sessions[0].WorkspaceManaged {
		t.Fatalf("failed managed launch lost its session: %+v, %v", sessions, err)
	}
	session := sessions[0]
	file := filepath.Join(session.Workspace, "unfinished")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(t.Context(), store, nix, session.ID, ""); err == nil {
		t.Fatal("resume succeeded without Docker")
	}
	after, err := store.Session(t.Context(), session.ID)
	if err != nil || after.Storage != session.Storage || after.Workspace != session.Workspace {
		t.Fatalf("resume changed retained environment: %+v, %v", after, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatalf("failed resume changed workspace files: %q, %v", data, err)
	}
}

func TestRuntimeCancellationStopsContainer(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real Docker checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	image, err := images.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := OpenStore(t.Context(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "pact", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	launch, err := createRecord(t.Context(), store, "", Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	current := &Session{Record: launch, store: store}
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
	output, sink := io.Pipe()
	defer output.Close()
	defer sink.Close()
	observed := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(output)
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "ready" {
			observed <- fmt.Errorf("runtime did not become ready: %q, %v", line, err)
			stop()
			return
		}
		stop()
		_, err = io.Copy(io.Discard, reader)
		observed <- err
	}()
	type result struct {
		started bool
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		err := current.runContainer(runCtx, docker.ContainerConfig{
			Image: image, Labels: map[string]string{"me.alexghr.pact.test": strings.TrimPrefix(label, "me.alexghr.pact.test=")},
			TTY: true, Interactive: true,
			Network: "none", Entrypoint: "/bin/bash", Command: []string{"-euc", "printf 'ready\\n'; exec sleep infinity"},
		}, Terminal{
			Stdin: strings.NewReader(""), Stdout: sink, Stderr: sink,
			Size: docker.TerminalSize{Rows: 24, Cols: 80}, Ready: func(string) {},
		})
		_ = sink.Close()
		current.runErr, current.interrupted = err, runCtx.Err() != nil
		cleanupErr := current.Cleanup()
		finished <- result{current.started, errors.Join(err, cleanupErr)}
	}()
	select {
	case result := <-finished:
		if !result.started || result.err == nil {
			t.Fatalf("interrupted runtime: started %v, %v", result.started, result.err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled runtime did not release its terminal stream")
	}
	select {
	case err := <-observed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("terminal output reader did not stop")
	}
	if ids, err := containers(ctx); err != nil || ids != "" {
		t.Fatalf("container survived cancellation: %q, %v", ids, err)
	}
}
