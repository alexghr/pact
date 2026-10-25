package execution

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

// Real Docker covers a launcher dying after creation but before recording the
// container ID, and proves that releasing an OS lock cannot authorize a writer.
func TestRecovery(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real Docker recovery")
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
	owner := &Owner{Store: store, Launch: launch}
	defer func() {
		if err := owner.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	const image = "ubuntu:26.04@sha256:da6fc2be547864451aa253836dd926da33623312df4a9a243e35dc877c378a78"
	// Stop precisely at the creation/bookkeeping boundary, using the real daemon.
	r, labels, err := owner.reserve(ctx, "container", "pact-recovery-", image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.CreateContainer(ctx, docker.ContainerConfig{
		Name: r.Name, Labels: labels, Network: "none", Image: image, Command: []string{"sleep", "infinity"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := docker.StartContainer(ctx, r.Name); err != nil {
		t.Fatal(err)
	}
	resumed, _, err := store.ResumeLaunch(ctx, launch.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	replacement := &Owner{Store: store, Launch: resumed}
	defer func() {
		if err := replacement.Cleanup(); err != nil {
			t.Error(err)
		}
	}()
	unlock, err := store.LockExecution(resumed)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := replacement.Recover(ctx); err == nil || !strings.Contains(err.Error(), r.Name) {
		t.Fatalf("running writer was replaced or unidentified: %v", err)
	}
	session, err := store.Session(ctx, launch.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Recover annotates the old execution, not the explicitly requested new one.
	if session.Status != "starting" {
		t.Fatalf("recovery changed the replacement execution: %+v", session)
	}
	if err := exec.CommandContext(ctx, "docker", "kill", r.Name).Run(); err != nil {
		t.Fatal(err)
	}
	t.Run("unreachable daemon", func(t *testing.T) {
		t.Setenv("DOCKER_CONTEXT", "")
		t.Setenv("DOCKER_HOST", "unix://"+t.TempDir()+"/missing.sock")
		if err := replacement.Recover(ctx); err == nil {
			t.Fatal("unreachable Docker daemon was treated as absence")
		}
	})
	if err := replacement.Recover(ctx); err != nil {
		t.Fatalf("stopped writer could not be recovered: %v", err)
	}
	if resources, err := store.Resources(ctx, launch.ExecutionID); err != nil || len(resources) != 0 {
		t.Fatalf("recovered resources retained: %+v, %v", resources, err)
	}
	created, err := replacement.CreateContainer(ctx, "pact-recovery-", docker.ContainerConfig{Image: image, Network: "none", Command: []string{"true"}})
	if err != nil || created.ContainerID == "" || created.ImageID == "" {
		t.Fatalf("actual runtime identity not recorded: %+v, %v", created, err)
	}
	resources, err := store.Resources(ctx, resumed.ExecutionID)
	if err != nil || len(resources) != 1 || resources[0] != created {
		t.Fatalf("actual Docker identity was not persisted: %+v, %v", resources, err)
	}
	if err := replacement.Cleanup(); err != nil {
		t.Fatal(err)
	}
	t.Run("before resource creation", func(t *testing.T) {
		t.Setenv("DOCKER_CONTEXT", "")
		t.Setenv("DOCKER_HOST", "unix://"+t.TempDir()+"/missing.sock")
		early, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		next, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: early.Workspace, Backend: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		owner := &Owner{Store: store, Launch: next}
		if err := owner.Recover(ctx); err != nil {
			t.Fatalf("crash before creation required Docker inspection: %v", err)
		}
		pending, err := store.PendingExecutions(ctx, next)
		if err != nil || len(pending) != 0 {
			t.Fatalf("execution without resources was not recovered: %v, %v", pending, err)
		}
	})
}
