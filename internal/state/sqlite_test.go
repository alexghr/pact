package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "pact.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})

	id, err := store.StartRun(ctx, Run{
		WorkspaceDir:      "/tmp/project",
		Model:             "gpt-5.6-sol",
		Effort:            "low",
		DockerfileVariant: "go",
	})
	if err != nil {
		t.Fatal(err)
	}

	run := readRun(t, store.db, id)
	if run.workspaceDir != "/tmp/project" || run.model != "gpt-5.6-sol" || run.effort != "low" || run.variant != "go" {
		t.Fatalf("stored run metadata = %#v", run)
	}
	if run.status != "running" || run.exitCode.Valid || run.completedAt.Valid {
		t.Fatalf("new run state = %#v", run)
	}
	if _, err := time.Parse(time.RFC3339Nano, run.createdAt); err != nil {
		t.Fatalf("created_at %q is not RFC3339: %v", run.createdAt, err)
	}

	exitCode := 0
	if err := store.CompleteRun(ctx, id, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	run = readRun(t, store.db, id)
	if run.status != "finished" || !run.exitCode.Valid || run.exitCode.Int64 != 0 || !run.completedAt.Valid {
		t.Fatalf("completed run state = %#v", run)
	}
	if _, err := time.Parse(time.RFC3339Nano, run.completedAt.String); err != nil {
		t.Fatalf("completed_at %q is not RFC3339: %v", run.completedAt.String, err)
	}
}

func TestRunErrorWithoutExitCode(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	id, err := store.StartRun(ctx, Run{WorkspaceDir: "/tmp/project", Model: "model", Effort: "low", DockerfileVariant: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRun(ctx, id, "error", nil); err != nil {
		t.Fatal(err)
	}

	run := readRun(t, store.db, id)
	if run.status != "error" || run.exitCode.Valid || !run.completedAt.Valid {
		t.Fatalf("failed run state = %#v", run)
	}
}

func TestCompleteRunRejectsInvalidTransition(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	id, err := store.StartRun(ctx, Run{WorkspaceDir: "/tmp/project", Model: "model", Effort: "low", DockerfileVariant: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if err := store.CompleteRun(ctx, id, "running", &exitCode); err == nil {
		t.Fatal("CompleteRun accepted running as a terminal status")
	}

	run := readRun(t, store.db, id)
	if run.status != "running" || run.completedAt.Valid {
		t.Fatalf("rejected transition changed run = %#v", run)
	}
}

func TestOpenCreatesPrivateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "pact.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("database permissions = %04o, want 0600", got)
	}
}

type storedRun struct {
	workspaceDir string
	model        string
	effort       string
	variant      string
	status       string
	exitCode     sql.NullInt64
	createdAt    string
	completedAt  sql.NullString
}

func readRun(t *testing.T, db *sql.DB, id int64) storedRun {
	t.Helper()
	var run storedRun
	err := db.QueryRow(`
		SELECT workspace_dir, model, effort, dockerfile_variant, status,
			exit_code, created_at, completed_at
		FROM runs WHERE id = ?`, id).Scan(
		&run.workspaceDir,
		&run.model,
		&run.effort,
		&run.variant,
		&run.status,
		&run.exitCode,
		&run.createdAt,
		&run.completedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	return run
}
