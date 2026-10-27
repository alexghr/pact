package session

import (
	"fmt"
	"github.com/alexghr/pact/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	launch, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Backend: "codex", Owner: fmt.Sprintf("uid:%d", os.Getuid())})
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
	if err := RemoveWorkspace(t.Context(), store, launch.Storage.WorkspaceID); err == nil {
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
	if err := RemoveWorkspace(t.Context(), store, launch.Storage.WorkspaceID); err == nil {
		t.Fatal("removal succeeded despite protected contents")
	}
	session, err := store.Session(t.Context(), launch.SessionID)
	if err != nil || session.WorkspaceStatus != "cleanupPending" {
		t.Fatalf("partial deletion lost intent: %+v, %v", session, err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), launch.SessionID); err == nil {
		t.Fatal("resume wrote into partially removed workspace")
	}
	if _, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: launch.Workspace, Backend: "codex"}); err == nil {
		t.Fatal("new session accepted partially removed workspace")
	}
	if err := os.Chmod(protected, 0700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.Storage.WorkspaceID); err != nil {
		t.Fatalf("could not retry partial deletion: %v", err)
	}
	// Reproduce an interruption after filesystem removal but before recording
	// completion. Retrying must finish the intent without allocating a directory.
	w, err := store.Workspace(t.Context(), launch.Storage.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	w.Status = "cleanupPending"
	if err := store.UpdateWorkspace(t.Context(), w); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.Storage.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResumeLaunch(t.Context(), launch.SessionID); err == nil || !strings.Contains(err.Error(), "intentionally removed") {
		t.Fatalf("removed workspace was recreated or unexplained: %v", err)
	}
	if err := os.Mkdir(launch.Workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launch.Workspace, "replacement"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorkspace(t.Context(), store, launch.Storage.WorkspaceID); err == nil {
		t.Fatal("repeated removal deleted a replacement directory")
	}
}
