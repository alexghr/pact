package state

import (
	"crypto/rand"
	"testing"
)

func createSession(t *testing.T, store *Store, path string) Session {
	t.Helper()
	record, err := store.CreateSession(t.Context(), Workspace{Path: path, Owner: "operator", Status: "ready"},
		Session{Backend: "codex", Storage: Storage{Key: rand.Text(), StateVolume: "private"}})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSessionAllocation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	first, second := openStore(t), openStore(t)
	workspace := "/workspace"
	type result struct {
		record Session
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, store := range []*Store{first, second} {
		go func() {
			<-start
			record, err := store.CreateSession(t.Context(), Workspace{Path: workspace, Owner: "operator", Status: "ready"},
				Session{Backend: "codex", Storage: Storage{Key: rand.Text()}})
			results <- result{record, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent allocation: %v, %v", a.err, b.err)
	}
	if a.record.ID > b.record.ID {
		a, b = b, a
	}
	if a.record.ID != 1 || b.record.ID <= a.record.ID {
		t.Fatalf("session IDs = %d, %d, want distinct increasing IDs starting at 1", a.record.ID, b.record.ID)
	}
	if a.record.WorkspaceID != b.record.WorkspaceID || a.record.ExecutionID == b.record.ExecutionID {
		t.Fatal("sessions must share the workspace registration but have distinct executions")
	}
	// Deleting the newest session must not make its issued ID available again.
	if _, err := first.db.ExecContext(t.Context(), "DELETE FROM executions WHERE session_id = ?", b.record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(t.Context(), "DELETE FROM sessions WHERE id = ?", b.record.ID); err != nil {
		t.Fatal(err)
	}
	third := createSession(t, first, workspace)
	if third.ID <= b.record.ID {
		t.Fatalf("session ID %d reused an issued ID", third.ID)
	}
}

func TestSessionAllocationRollsBack(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	// An invalid backend fails the session insert after workspace insertion.
	_, err := store.CreateSession(t.Context(), Workspace{Path: "/workspace", Owner: "operator", Status: "ready"}, Session{})
	if err == nil {
		t.Fatal("session with an empty backend succeeded")
	}
	for _, table := range []string{"workspaces", "sessions", "executions"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed allocation left %d records in %s", count, table)
		}
	}
}
