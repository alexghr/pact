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
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

func TestManagedLaunchFailureCanResume(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A real unreachable endpoint fails startup before any agent can run.
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nix, err := store.EnsureSharedStorage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), store, nix, Options{Title: "Investigate deployment errors", Model: "saved", Capabilities: state.Capabilities{DIND: true, HostNetwork: true}}); err == nil {
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
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	launch, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Session(t.Context(), launch.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	current := &Session{Record: record, store: store, launch: launch}
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
		err := current.runContainer(runCtx, docker.ContainerConfig{
			Image: image, Labels: map[string]string{"me.alexghr.pact.test": strings.TrimPrefix(label, "me.alexghr.pact.test=")},
			Network: "none", Entrypoint: "/bin/bash", Command: []string{"-euc", "printf 'ready\\n'; exec sleep infinity"},
		}, func(input io.Reader) error {
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
