package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeRetainsEnvironmentAndRejectsMovedWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: workspace, Backend: "codex",
		Settings: LaunchSettings{Model: "original", Effort: "high", Prompted: true, DinD: true, HostNet: true}})
	if err != nil {
		t.Fatal(err)
	}
	resumed, settings, err := store.ResumeLaunch(t.Context(), original.SessionID, false)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != original.SessionID || resumed.Environment != original.Environment || resumed.Workspace != original.Workspace || resumed.ExecutionID <= original.ExecutionID {
		t.Fatalf("resume allocated new state or reused execution: %+v", resumed)
	}
	if settings.Prompted || settings.Model != "original" || settings.Effort != "high" || !settings.DinD || !settings.HostNet {
		t.Fatalf("resume did not retain interactive settings: %+v", settings)
	}
	prompted, settings, err := store.ResumeLaunch(t.Context(), original.SessionID, true)
	if err != nil || prompted.SessionID != original.SessionID || prompted.Environment != original.Environment || !settings.Prompted {
		t.Fatalf("prompted continuation lost session, environment, or mode: %+v, %+v, %v", prompted, settings, err)
	}
	if err := os.Rename(workspace, workspace+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workspace+"-moved", workspace); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), original.SessionID, false); err == nil {
		t.Fatal("resume followed a redirected saved workspace")
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM executions").Scan(&count); err != nil || count != 3 {
		t.Fatalf("invalid workspace created an execution: count %d, %v", count, err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), original.SessionID+1, true); err == nil {
		t.Fatal("resume invented a missing session")
	}
}
