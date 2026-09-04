package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionAndRunLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	sessionID := createTestSession(t, store, "/tmp/project")
	if sessionID != 1 {
		t.Fatalf("first session ID = %d, want 1", sessionID)
	}
	if secondID := createTestSession(t, store, "/tmp/other"); secondID != 2 {
		t.Fatalf("second session ID = %d, want 2", secondID)
	}

	session, err := store.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != sessionID || session.WorkspaceDir != "/tmp/project" ||
		session.Status != "pending" || session.LatestRunID != 0 || session.ThreadID != "" {
		t.Fatalf("new session = %#v", session)
	}

	runID := startTestRun(t, store, sessionID, "gpt-5.6-sol", "low", "go")
	run := readRun(t, store.db, runID)
	if run.pactSessionID != sessionID || run.model != "gpt-5.6-sol" ||
		run.effort != "low" || run.variant != "go" || run.status != "running" ||
		run.exitCode.Valid || run.completedAt.Valid {
		t.Fatalf("new run = %#v", run)
	}
	if _, err := time.Parse(time.RFC3339Nano, run.createdAt); err != nil {
		t.Fatalf("created_at %q is not RFC3339: %v", run.createdAt, err)
	}

	exitCode := 0
	if err := store.CompleteRun(ctx, runID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}
	run = readRun(t, store.db, runID)
	if run.status != "finished" || !run.exitCode.Valid || run.exitCode.Int64 != 0 || !run.completedAt.Valid {
		t.Fatalf("completed run = %#v", run)
	}
}

func TestStartRunRequiresSession(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.StartRun(context.Background(), Run{
		PactSessionID:     99,
		Model:             "model",
		Effort:            "low",
		DockerfileVariant: "generic",
	}); err == nil {
		t.Fatal("StartRun succeeded for an unknown Pact session")
	}
}

func TestListRuns(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	finishedSessionID := createTestSession(t, store, "/tmp/finished")
	finishedID := startTestRun(t, store, finishedSessionID, "model-a", "low", "generic")
	exitCode := 0
	if err := store.CompleteRun(ctx, finishedID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	runningSessionID := createTestSession(t, store, "/tmp/running")
	runningID := startTestRun(t, store, runningSessionID, "model-b", "high", "go")

	runs, err := store.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("ListRuns() returned %d runs, want 2", len(runs))
	}
	if got := runs[0]; got.ID != runningID || got.PactSessionID != runningSessionID ||
		got.Status != "running" || got.WorkspaceDir != "/tmp/running" || got.Model != "model-b" {
		t.Fatalf("newest run = %#v", got)
	}
	if got := runs[1]; got.ID != finishedID || got.PactSessionID != finishedSessionID ||
		got.Status != "finished" {
		t.Fatalf("finished run = %#v", got)
	}
}

func TestSessionOwnsRunsAndMultipleThreads(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	sessionID := createTestSession(t, store, "/tmp/session")

	firstRunID := startTestRun(t, store, sessionID, "model-a", "low", "generic")
	firstThread := CodexThread{
		ThreadID:    "thr_1",
		SessionID:   "codex_session_1",
		UserAgent:   "codex/0.149.0",
		StateVolume: "pact-codex-state-1-1",
	}
	if err := store.LinkCodexThread(ctx, firstRunID, firstThread); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendCodexEvent(ctx, firstRunID, 1, "turn/completed", []byte(`{"turn":{"id":"turn_1"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreCodexTranscript(ctx, firstRunID, []byte(`{
		"thread":{"turns":[{"items":[{"type":"agentMessage","text":"first answer"}]}]}
	}`)); err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	if err := store.CompleteRun(ctx, firstRunID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	resumedRunID := startTestRun(t, store, sessionID, "model-b", "high", "go")
	firstThread.UserAgent = "codex/0.150.0"
	if err := store.LinkCodexThread(ctx, resumedRunID, firstThread); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendCodexEvent(ctx, resumedRunID, 1, "turn/completed", []byte(`{"turn":{"id":"turn_2"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRun(ctx, resumedRunID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	secondThreadRunID := startTestRun(t, store, sessionID, "model-c", "medium", "generic")
	if err := store.LinkCodexThread(ctx, secondThreadRunID, CodexThread{
		ThreadID:    "thr_2",
		SessionID:   "codex_session_2",
		UserAgent:   "codex/0.150.0",
		StateVolume: "pact-codex-state-1-3",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendCodexEvent(ctx, secondThreadRunID, 1, "turn/completed", []byte(`{"turn":{"id":"turn_3"}}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreCodexTranscript(ctx, secondThreadRunID, []byte(`{
		"thread":{"turns":[{"items":[{"type":"agentMessage","text":"second thread answer"}]}]}
	}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteRun(ctx, secondThreadRunID, "finished", &exitCode); err != nil {
		t.Fatal(err)
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListSessions() returned %d sessions, want 1", len(sessions))
	}
	if got := sessions[0]; got.ID != sessionID || got.LatestRunID != secondThreadRunID ||
		got.ThreadID != "thr_2" || got.CodexSessionID != "codex_session_2" ||
		got.Model != "model-c" || got.LastAgentMessage != "second thread answer" {
		t.Fatalf("ListSessions()[0] = %#v", got)
	}

	events, err := store.ListSessionEvents(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].RunID != firstRunID ||
		events[1].RunID != resumedRunID || events[2].RunID != secondThreadRunID {
		t.Fatalf("ListSessionEvents() = %#v", events)
	}

	target, err := store.GetResumeTarget(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if target.PactSessionID != sessionID || target.RunID != secondThreadRunID ||
		target.ThreadID != "thr_2" || target.StateVolume != "pact-codex-state-1-3" ||
		target.WorkspaceDir != "/tmp/session" || target.Model != "model-c" {
		t.Fatalf("GetResumeTarget() = %#v", target)
	}
}

func TestLinkCodexThreadRejectsCrossSessionReuse(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	firstSessionID := createTestSession(t, store, "/tmp/one")
	firstRunID := startTestRun(t, store, firstSessionID, "model", "low", "generic")
	thread := CodexThread{ThreadID: "thr_1", SessionID: "codex_1", UserAgent: "codex", StateVolume: "volume-1"}
	if err := store.LinkCodexThread(ctx, firstRunID, thread); err != nil {
		t.Fatal(err)
	}

	secondSessionID := createTestSession(t, store, "/tmp/two")
	secondRunID := startTestRun(t, store, secondSessionID, "model", "low", "generic")
	if err := store.LinkCodexThread(ctx, secondRunID, thread); err == nil {
		t.Fatal("LinkCodexThread reused a thread across Pact sessions")
	}
}

func TestGetResumeTargetRequiresLinkedThread(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	sessionID := createTestSession(t, store, "/tmp/project")
	startTestRun(t, store, sessionID, "model", "low", "generic")
	if _, err := store.GetResumeTarget(ctx, sessionID); !errors.Is(err, ErrResumeTargetNotFound) {
		t.Fatalf("GetResumeTarget() error = %v, want ErrResumeTargetNotFound", err)
	}
	if _, err := store.GetResumeTarget(ctx, 99); !errors.Is(err, ErrResumeTargetNotFound) {
		t.Fatalf("GetResumeTarget(unknown) error = %v, want ErrResumeTargetNotFound", err)
	}
}

func TestCompleteRunRejectsInvalidTransition(t *testing.T) {
	store := openTestStore(t)
	sessionID := createTestSession(t, store, "/tmp/project")
	runID := startTestRun(t, store, sessionID, "model", "low", "generic")
	exitCode := 0
	if err := store.CompleteRun(context.Background(), runID, "running", &exitCode); err == nil {
		t.Fatal("CompleteRun accepted running as a terminal status")
	}
	if run := readRun(t, store.db, runID); run.status != "running" || run.completedAt.Valid {
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

func TestMigrateAppliesEachMigrationOnce(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})

	var table string
	err = store.db.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name = 'pact_sessions'`).Scan(&table)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pact_sessions before Migrate() error = %v, want sql.ErrNoRows", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID := createTestSession(t, store, "/tmp/project")
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var migrationCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("recorded migrations = %d, want 1", migrationCount)
	}
	if session, err := store.GetSession(ctx, sessionID); err != nil || session.WorkspaceDir != "/tmp/project" {
		t.Fatalf("session after second Migrate() = %#v, %v", session, err)
	}
}

func TestMigrateAdoptsExistingSchema(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})

	initial, err := migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, string(initial)); err != nil {
		t.Fatal(err)
	}
	result, err := store.db.ExecContext(ctx, `
		INSERT INTO pact_sessions (workspace_dir) VALUES ('/tmp/existing')`)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if session, err := store.GetSession(ctx, sessionID); err != nil || session.WorkspaceDir != "/tmp/existing" {
		t.Fatalf("existing session after Migrate() = %#v, %v", session, err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func createTestSession(t *testing.T, store *Store, workspace string) int64 {
	t.Helper()
	id, err := store.CreateSession(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func startTestRun(t *testing.T, store *Store, sessionID int64, model, effort, variant string) int64 {
	t.Helper()
	id, err := store.StartRun(context.Background(), Run{
		PactSessionID:     sessionID,
		Model:             model,
		Effort:            effort,
		DockerfileVariant: variant,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type storedRun struct {
	pactSessionID int64
	model         string
	effort        string
	variant       string
	status        string
	exitCode      sql.NullInt64
	createdAt     string
	completedAt   sql.NullString
}

func readRun(t *testing.T, db *sql.DB, id int64) storedRun {
	t.Helper()
	var run storedRun
	err := db.QueryRow(`
		SELECT pact_session_id, model, effort, dockerfile_variant, status,
			exit_code, created_at, completed_at
		FROM runs WHERE id = ?`, id).Scan(
		&run.pactSessionID,
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
