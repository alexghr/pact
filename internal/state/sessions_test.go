package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchAllocation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	first, second := openStore(t), openStore(t)
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	options := LaunchOptions{Owner: "operator", Backend: "codex"}
	type result struct {
		launch Launch
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i, store := range []*Store{first, second} {
		go func() {
			<-start
			opts := options
			opts.Workspace = []string{workspace, alias}[i]
			launch, err := store.CreateLaunch(t.Context(), opts)
			results <- result{launch, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent allocation: %v, %v", a.err, b.err)
	}
	if a.launch.SessionID > b.launch.SessionID {
		a, b = b, a
	}
	if a.launch.SessionID != 1 || b.launch.SessionID <= a.launch.SessionID {
		t.Fatalf("session IDs = %d, %d, want distinct increasing IDs starting at 1", a.launch.SessionID, b.launch.SessionID)
	}
	if a.launch.Environment.WorkspaceID != b.launch.Environment.WorkspaceID {
		t.Fatal("workspace alias received a separate registration")
	}
	if a.launch.Environment.ID == b.launch.Environment.ID || a.launch.ExecutionID == b.launch.ExecutionID {
		t.Fatal("independent launches share environment or execution identity")
	}
	// Deleting the newest session must not make its issued ID available again.
	if _, err := first.db.ExecContext(t.Context(), "DELETE FROM executions WHERE session_id = ?", b.launch.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(t.Context(), "DELETE FROM sessions WHERE id = ?", b.launch.SessionID); err != nil {
		t.Fatal(err)
	}
	options.Workspace = workspace
	third, err := first.CreateLaunch(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if third.SessionID <= b.launch.SessionID {
		t.Fatalf("session ID %d reused an issued ID", third.SessionID)
	}
}

func TestLaunchAllocationRollsBack(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	// An invalid backend fails the session insert after workspace/environment insertion.
	_, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Owner: "operator"})
	if err == nil {
		t.Fatal("launch with an empty backend succeeded")
	}
	for _, table := range []string{"workspaces", "environments", "sessions", "executions"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed launch left %d records in %s", count, table)
		}
	}
}
