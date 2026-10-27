package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func openSessionStore(t *testing.T) *state.Store {
	t.Helper()
	store, err := OpenStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestWorkspaceRemovalRecovery(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openSessionStore(t)
	launch, err := createRecord(t.Context(), store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// A retained path redirected to an unrelated directory is never a deletion
	// target, even when the catalogue still records managed ownership.
	if err := os.Rename(launch.Workspace, launch.Workspace+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), launch.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.WorkspaceID); err == nil {
		t.Fatal("cleanup followed a redirected workspace")
	}
	if err := os.Remove(launch.Workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(launch.Workspace+"-original", launch.Workspace); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(launch.Workspace, "protected")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(protected, "file"), []byte("keep until retry"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(protected, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(protected, 0700) })
	if err := RemoveWorkspace(t.Context(), store, launch.WorkspaceID); err == nil {
		t.Fatal("removal succeeded despite protected contents")
	}
	session, err := store.Session(t.Context(), launch.ID)
	if err != nil || session.WorkspaceStatus != "cleanupPending" {
		t.Fatalf("partial deletion lost intent: %+v, %v", session, err)
	}
	if _, err := restoreRecord(t.Context(), store, launch.ID); err == nil {
		t.Fatal("resume wrote into partially removed workspace")
	}
	if _, err := createRecord(t.Context(), store, Options{Workspace: launch.Workspace}); err == nil {
		t.Fatal("new session accepted partially removed workspace")
	}
	if err := os.Chmod(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.WorkspaceID); err != nil {
		t.Fatalf("could not retry partial deletion: %v", err)
	}
	// Reproduce an interruption after filesystem removal but before recording
	// completion. Retrying must finish the intent without allocating a directory.
	w, err := store.Workspace(t.Context(), launch.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	w.Status = "cleanupPending"
	if err := store.UpdateWorkspace(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreRecord(t.Context(), store, launch.ID); err == nil || !strings.Contains(err.Error(), "intentionally removed") {
		t.Fatalf("removed workspace was recreated or unexplained: %v", err)
	}
	if err := os.Mkdir(launch.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launch.Workspace, "replacement"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.WorkspaceID); err == nil {
		t.Fatal("repeated removal deleted a replacement directory")
	}
}

func TestManagedWorkspaceLifecycle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, other := openSessionStore(t), openSessionStore(t)
	results := make(chan state.Session, 2)
	for _, connection := range []*state.Store{store, other} {
		go func() {
			launch, err := createRecord(t.Context(), connection, Options{})
			if err != nil {
				t.Error(err)
			}
			results <- launch
		}()
	}
	a, b := <-results, <-results
	if t.Failed() {
		t.FailNow()
	}
	if a.Workspace == b.Workspace || a.Storage.StateVolume == b.Storage.StateVolume {
		t.Fatal("independent launches share workspace or agent state")
	}
	for _, launch := range []state.Session{a, b} {
		entries, err := os.ReadDir(launch.Workspace)
		if err != nil || len(entries) != 0 {
			t.Fatalf("workspace was not initially empty: %v, %v", entries, err)
		}
		info, err := os.Stat(launch.Workspace)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("workspace is not private: %v, %v", info, err)
		}
	}
	repo := filepath.Join(a.Workspace, "project")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git("init", "-b", "main")
	for _, step := range []struct{ branch, text string }{{"main", "base"}, {"side", "side"}, {"main", "main"}} {
		if step.branch == "side" {
			git("checkout", "-b", "side")
		} else if step.text != "base" {
			git("checkout", "main")
		}
		if err := os.WriteFile(filepath.Join(repo, "file"), []byte(step.text), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", "file")
		git("commit", "-m", step.text)
	}
	output, err := exec.CommandContext(t.Context(), "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "-C", repo, "merge", "side").CombinedOutput()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected an unfinished conflicting merge: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(a.Workspace, "build-output"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	before := git("status", "--porcelain") + git("rev-parse", "HEAD", "MERGE_HEAD")
	if err := store.FinishExecution(t.Context(), a.ExecutionID, state.StartupFailed, 1, "runtime unavailable"); err != nil {
		t.Fatal(err)
	}
	resumed, err := restoreRecord(t.Context(), store, a.ID)
	if err != nil || resumed.Workspace != a.Workspace || resumed.Storage != a.Storage {
		t.Fatalf("resume lost workspace or environment: %+v, %v", resumed, err)
	}
	if after := git("status", "--porcelain") + git("rev-parse", "HEAD", "MERGE_HEAD"); after != before {
		t.Fatal("resume changed local commits or unfinished Git state")
	}
	if data, err := os.ReadFile(filepath.Join(resumed.Workspace, "build-output")); err != nil || string(data) != "retained" {
		t.Fatalf("resume lost untracked build output: %q, %v", data, err)
	}
	if err := os.Rename(a.Workspace, a.Workspace+"-original"); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreRecord(t.Context(), store, a.ID); err == nil {
		t.Fatal("resume replaced a missing retained directory")
	}
	if err := os.Mkdir(a.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreRecord(t.Context(), store, a.ID); err == nil {
		t.Fatal("resume accepted a replacement directory")
	}
}

func TestManagedAllocationFailureIsRetained(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openSessionStore(t)
	root := filepath.Join(filepath.Dir(store.Path()), "workspaces")
	if err := os.Mkdir(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0700) })
	_, err := createRecord(t.Context(), store, Options{})
	if err == nil || !strings.Contains(err.Error(), "needs recovery") {
		t.Fatalf("allocation failure: %v", err)
	}
	sessions, err := store.Sessions(t.Context(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "startupFailed" || !sessions[0].WorkspaceManaged || sessions[0].WorkspaceStatus != "allocating" {
		t.Fatalf("failure lost allocation records: %+v, %v", sessions, err)
	}
	if _, err := restoreRecord(t.Context(), store, sessions[0].ID); err == nil {
		t.Fatal("resume silently completed a failed allocation")
	}
}

func TestResumeRetainsSettingsAndRejectsMovedWorkspace(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openSessionStore(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := createRecord(t.Context(), store, Options{Workspace: workspace, Model: "original", Effort: "high", Capabilities: Capabilities{DIND: true, HostNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restoreRecord(t.Context(), store, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != original.ID || resumed.Storage != original.Storage || resumed.Workspace != original.Workspace || resumed.ExecutionID <= original.ExecutionID {
		t.Fatalf("resume allocated new state or reused execution: %+v", resumed)
	}
	if resumed.Settings.Model != "original" || resumed.Settings.Effort != "high" || !resumed.Settings.Capabilities.DIND || !resumed.Settings.Capabilities.HostNetwork {
		t.Fatalf("resume did not retain interactive settings: %+v", resumed.Settings)
	}
	next, err := restoreRecord(t.Context(), store, original.ID)
	if err != nil || next.ID != original.ID || next.Storage != original.Storage {
		t.Fatalf("repeated restore lost session or storage: %+v, %+v, %v", next, next.Settings, err)
	}
	// Finishing an older run must not finish the newer one of the same session.
	previous := &Session{Record: resumed, store: store}
	if err := previous.Cleanup(); err != nil {
		t.Fatal(err)
	}
	latest, err := store.Session(t.Context(), original.ID)
	if err != nil || latest.ExecutionID != next.ExecutionID || latest.Status != "starting" {
		t.Fatalf("older cleanup changed the current run: %+v, %v", latest, err)
	}
	if err := os.Rename(workspace, workspace+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workspace+"-moved", workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreRecord(t.Context(), store, original.ID); err == nil {
		t.Fatal("resume followed a redirected saved workspace")
	}
	after, err := store.Session(t.Context(), original.ID)
	if err != nil || after.ExecutionID != next.ExecutionID {
		t.Fatalf("invalid workspace created an execution: %+v, %v", after, err)
	}
	if _, err := restoreRecord(t.Context(), store, original.ID+1); err == nil {
		t.Fatal("resume invented a missing session")
	}
}
