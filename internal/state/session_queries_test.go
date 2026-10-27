package state

import (
	"testing"
)

func TestSessionListing(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	workspace := t.TempDir()
	first, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: workspace, Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishExecution(t.Context(), first.ExecutionID, Failed, 1, "original failure"); err != nil {
		t.Fatal(err)
	}
	// A subsequent execution is the displayed state, even if the first failed.
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO executions(session_id, settings, status, created_at)
		VALUES (?, '{"model":"continued","prompted":false}', 'running', 1)`, first.SessionID); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.Sessions(t.Context(), workspace)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("workspace listing: %v, %v", sessions, err)
	}
	if sessions[0].ID != first.SessionID || sessions[0].Status != "running" || sessions[0].Error != "" || sessions[0].Settings.Model != "continued" {
		t.Fatalf("listing did not select latest execution: %+v", sessions[0])
	}
	all, err := store.Sessions(t.Context(), "")
	if err != nil || len(all) != 2 || all[0].ID != other.SessionID {
		t.Fatalf("all-workspaces listing: %v, %v", all, err)
	}
	session, err := store.Session(t.Context(), first.SessionID)
	if err != nil || session.Storage != first.Storage {
		t.Fatalf("saved session environment changed: %+v, %v", session, err)
	}
}
