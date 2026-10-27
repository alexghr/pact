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
		Settings: LaunchSettings{Model: "original", Effort: "high", Capabilities: Capabilities{DIND: true, HostNetwork: true}}})
	if err != nil {
		t.Fatal(err)
	}
	resumed, settings, err := store.ResumeLaunch(t.Context(), original.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != original.SessionID || resumed.Storage != original.Storage || resumed.Workspace != original.Workspace || resumed.ExecutionID <= original.ExecutionID {
		t.Fatalf("resume allocated new state or reused execution: %+v", resumed)
	}
	if settings.Model != "original" || settings.Effort != "high" || !settings.Capabilities.DIND || !settings.Capabilities.HostNetwork {
		t.Fatalf("resume did not retain interactive settings: %+v", settings)
	}
	prompted, settings, err := store.ResumeLaunch(t.Context(), original.SessionID)
	if err != nil || prompted.SessionID != original.SessionID || prompted.Storage != original.Storage {
		t.Fatalf("prompted continuation lost session, environment, or mode: %+v, %+v, %v", prompted, settings, err)
	}
	if err := os.Rename(workspace, workspace+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workspace+"-moved", workspace); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), original.SessionID); err == nil {
		t.Fatal("resume followed a redirected saved workspace")
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM executions").Scan(&count); err != nil || count != 3 {
		t.Fatalf("invalid workspace created an execution: count %d, %v", count, err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), original.SessionID+1); err == nil {
		t.Fatal("resume invented a missing session")
	}
}
