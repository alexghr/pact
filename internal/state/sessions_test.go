package state

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if a.launch.Environment.StateVolume == b.launch.Environment.StateVolume || a.launch.Environment.NixVolume != b.launch.Environment.NixVolume {
		t.Fatal("independent environments must have private state and one shared store")
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

func TestStorageReclamationReferences(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	opts := LaunchOptions{Workspace: t.TempDir(), Owner: "operator", Backend: "codex"}
	launch, err := store.CreateLaunch(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishExecution(t.Context(), launch.ExecutionID, StartupFailed, 1, "interrupted provisioning"); err != nil {
		t.Fatal(err)
	}
	stale, err := store.StaleEnvironments(t.Context())
	if err != nil || len(stale) != 0 {
		t.Fatalf("retained failed session eligible for GC: %v, %v", stale, err)
	}
	// A second retained session protects the same environment after the first
	// reference is removed. Completed executions do not imply abandonment.
	if _, err := store.db.ExecContext(t.Context(), `
		INSERT INTO sessions(environment_id, owner, backend, created_at) SELECT environment_id, owner, backend, created_at FROM sessions;
		DELETE FROM executions;
		DELETE FROM sessions WHERE id = ?`, launch.SessionID); err != nil {
		t.Fatal(err)
	}
	stale, err = store.StaleEnvironments(t.Context())
	if err != nil || len(stale) != 0 {
		t.Fatalf("second session reference was ignored: %v, %v", stale, err)
	}
	if _, err := store.db.ExecContext(t.Context(), "DELETE FROM sessions"); err != nil {
		t.Fatal(err)
	}
	stale, err = store.StaleEnvironments(t.Context())
	if err != nil || len(stale) != 1 || stale[0].ID != launch.Environment.ID {
		t.Fatalf("unreferenced partial provision not reclaimable: %v, %v", stale, err)
	}
	// A different environment can still reference the volume in either role.
	other, err := store.CreateLaunch(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(t.Context(), `
		UPDATE environments SET nix_volume = ? WHERE id = ?`, launch.Environment.StateVolume, other.Environment.ID); err != nil {
		t.Fatal(err)
	}
	stale, err = store.StaleEnvironments(t.Context())
	if err != nil || len(stale) != 0 {
		t.Fatalf("cross-environment volume reference was ignored: %v, %v", stale, err)
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
	for _, table := range []string{"workspaces", "environments", "sessions", "executions", "shared_storage"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed launch left %d records in %s", count, table)
		}
	}
}

func TestManagedWorkspaceLifecycle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, other := openStore(t), openStore(t)
	results := make(chan Launch, 2)
	for _, connection := range []*Store{store, other} {
		go func() {
			launch, err := connection.CreateLaunch(t.Context(), LaunchOptions{Backend: "codex"})
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
	if a.Workspace == b.Workspace || a.Environment.StateVolume == b.Environment.StateVolume {
		t.Fatal("independent launches share workspace or agent state")
	}
	for _, launch := range []Launch{a, b} {
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
	if err := store.FinishExecution(t.Context(), a.ExecutionID, StartupFailed, 1, "runtime unavailable"); err != nil {
		t.Fatal(err)
	}
	resumed, _, err := store.ResumeLaunch(t.Context(), a.SessionID, false)
	if err != nil || resumed.Workspace != a.Workspace || resumed.Environment != a.Environment {
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
	if _, _, err := store.ResumeLaunch(t.Context(), a.SessionID, false); err == nil {
		t.Fatal("resume replaced a missing retained directory")
	}
	if err := os.Mkdir(a.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), a.SessionID, false); err == nil {
		t.Fatal("resume accepted a replacement directory")
	}
}

func TestManagedAllocationFailureIsRetained(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	root := filepath.Join(filepath.Dir(store.path), "workspaces")
	if err := os.Mkdir(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0700) })
	_, err := store.CreateLaunch(t.Context(), LaunchOptions{Backend: "codex"})
	if err == nil || !strings.Contains(err.Error(), "needs recovery") {
		t.Fatalf("allocation failure: %v", err)
	}
	sessions, err := store.Sessions(t.Context(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "startupFailed" || !sessions[0].WorkspaceManaged || sessions[0].WorkspaceStatus != "allocating" {
		t.Fatalf("failure lost allocation records: %+v, %v", sessions, err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), sessions[0].ID, false); err == nil {
		t.Fatal("resume silently completed a failed allocation")
	}
}

func TestWorkspaceRemovalRecovery(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	launch, err := store.CreateLaunch(t.Context(), LaunchOptions{Backend: "codex", Owner: fmt.Sprintf("uid:%d", os.Getuid())})
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
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err == nil {
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
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err == nil {
		t.Fatal("removal succeeded despite protected contents")
	}
	session, err := store.Session(t.Context(), launch.SessionID)
	if err != nil || session.WorkspaceStatus != "cleanupPending" {
		t.Fatalf("partial deletion lost intent: %+v, %v", session, err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), launch.SessionID, false); err == nil {
		t.Fatal("resume wrote into partially removed workspace")
	}
	if _, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: launch.Workspace, Backend: "codex"}); err == nil {
		t.Fatal("new session accepted partially removed workspace")
	}
	if err := os.Chmod(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err != nil {
		t.Fatalf("could not retry partial deletion: %v", err)
	}
	// Reproduce an interruption after filesystem removal but before recording
	// completion. Retrying must finish the intent without allocating a directory.
	if _, err := store.db.ExecContext(t.Context(), "UPDATE workspaces SET status = 'cleanupPending' WHERE id = ?", launch.Environment.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), launch.SessionID, false); err == nil || !strings.Contains(err.Error(), "intentionally removed") {
		t.Fatalf("removed workspace was recreated or unexplained: %v", err)
	}
	if err := os.Mkdir(launch.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launch.Workspace, "replacement"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveWorkspace(t.Context(), launch.Environment.WorkspaceID); err == nil {
		t.Fatal("repeated removal deleted a replacement directory")
	}
}
