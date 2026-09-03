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

func TestListRuns(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	finishedID, err := store.StartRun(ctx, Run{
		WorkspaceDir:      "/tmp/finished",
		Model:             "model-a",
		Effort:            "low",
		DockerfileVariant: "generic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartCodexSession(ctx, finishedID, CodexSession{
		ThreadID:    "thr_finished",
		SessionID:   "session_finished",
		UserAgent:   "codex/0.149.0",
		StateVolume: "pact-codex-state",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreCodexTranscript(ctx, finishedID, []byte(`{
		"thread": {"turns": [
			{"items": [{"type": "agentMessage", "text": "older answer"}]},
			{"items": [
				{"type": "agentMessage", "text": "commentary"},
				{"type": "agentMessage", "text": "latest answer"}
			]}
		]}
	}`)); err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if err := store.CompleteRun(ctx, finishedID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	runningID, err := store.StartRun(ctx, Run{
		WorkspaceDir:      "/tmp/running",
		Model:             "model-b",
		Effort:            "high",
		DockerfileVariant: "go",
	})
	if err != nil {
		t.Fatal(err)
	}

	runs, err := store.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRuns() returned %d runs, want 2", len(runs))
	}
	if got := runs[0]; got.ID != runningID || got.Status != "running" ||
		got.WorkspaceDir != "/tmp/running" || got.Model != "model-b" ||
		got.Effort != "high" || got.DockerfileVariant != "go" {
		t.Fatalf("newest run = %#v", got)
	}
	if got := runs[1]; got.ID != finishedID || got.Status != "finished" ||
		got.LastAgentMessage != "latest answer" {
		t.Fatalf("finished run = %#v", got)
	}
}

func TestCodexSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	runID, err := store.StartRun(ctx, Run{
		WorkspaceDir:      "/tmp/project",
		Model:             "model",
		Effort:            "low",
		DockerfileVariant: "generic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartCodexSession(ctx, runID, CodexSession{
		ThreadID:    "thr_123",
		SessionID:   "session_123",
		UserAgent:   "codex/0.149.0",
		StateVolume: "pact-codex-state",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendCodexEvent(ctx, runID, 1, "item/completed", []byte(`{"item":{"id":"item_1"}}`)); err != nil {
		t.Fatal(err)
	}
	transcript := []byte(`{"thread":{"id":"thr_123","turns":[]}}`)
	if err := store.StoreCodexTranscript(ctx, runID, transcript); err != nil {
		t.Fatal(err)
	}

	var threadID, sessionID, userAgent, stateVolume, transcriptJSON string
	var capturedAt sql.NullString
	err = store.db.QueryRow(`
		SELECT thread_id, session_id, user_agent, state_volume,
			transcript_json, transcript_captured_at
		FROM codex_sessions WHERE run_id = ?`, runID).Scan(
		&threadID,
		&sessionID,
		&userAgent,
		&stateVolume,
		&transcriptJSON,
		&capturedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if threadID != "thr_123" || sessionID != "session_123" ||
		userAgent != "codex/0.149.0" || stateVolume != "pact-codex-state" ||
		transcriptJSON != string(transcript) || !capturedAt.Valid {
		t.Fatalf("stored Codex session = %q %q %q %q %q %v",
			threadID, sessionID, userAgent, stateVolume, transcriptJSON, capturedAt)
	}

	var sequence int64
	var method, paramsJSON, receivedAt string
	err = store.db.QueryRow(`
		SELECT sequence, method, params_json, received_at
		FROM codex_events WHERE run_id = ?`, runID).Scan(
		&sequence,
		&method,
		&paramsJSON,
		&receivedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 1 || method != "item/completed" ||
		paramsJSON != `{"item":{"id":"item_1"}}` {
		t.Fatalf("stored Codex event = %d %q %q", sequence, method, paramsJSON)
	}
	if _, err := time.Parse(time.RFC3339Nano, receivedAt); err != nil {
		t.Fatalf("received_at %q is not RFC3339: %v", receivedAt, err)
	}
}

func TestGetResumeTarget(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	runID, err := store.StartRun(ctx, Run{
		WorkspaceDir:      "/tmp/project",
		Model:             "model",
		Effort:            "high",
		DockerfileVariant: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetResumeTarget(ctx, runID); err == nil {
		t.Fatal("GetResumeTarget succeeded before a Codex session was recorded")
	}

	if err := store.StartCodexSession(ctx, runID, CodexSession{
		ThreadID:    "thr_123",
		SessionID:   "session_123",
		UserAgent:   "codex/0.149.0",
		StateVolume: "pact-codex-state",
	}); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetResumeTarget(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if target.RunID != runID || target.WorkspaceDir != "/tmp/project" ||
		target.Model != "model" || target.Effort != "high" ||
		target.DockerfileVariant != "go" || target.ThreadID != "thr_123" ||
		target.SessionID != "session_123" || target.StateVolume != "pact-codex-state" {
		t.Fatalf("GetResumeTarget() = %#v", target)
	}
}

func TestGetResumeTargetRejectsUnknownRun(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if _, err := store.GetResumeTarget(ctx, 99); err == nil {
		t.Fatal("GetResumeTarget succeeded for an unknown run")
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
