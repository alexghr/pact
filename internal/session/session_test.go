package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestManagedLaunchFailureCanResume(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// A real unreachable endpoint fails startup before any agent can run.
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	store, err := OpenStore(t.Context(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "pact", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nix := Nix{Container: "pact-test-nix", Volume: "pact-test-nix-volume"}
	if _, err := New(t.Context(), store, nix, t.TempDir(), Options{Title: "Investigate deployment errors", Model: "saved", Capabilities: state.Capabilities{DIND: true, HostNetwork: true}}); err == nil {
		t.Fatal("launch succeeded without Docker")
	}
	sessions, err := store.Sessions(t.Context(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "startupFailed" || !sessions[0].WorkspaceManaged {
		t.Fatalf("failed managed launch lost its session: %+v, %v", sessions, err)
	}
	session := sessions[0]
	file := filepath.Join(session.Workspace, "unfinished")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(t.Context(), store, nix, session.ID, ""); err == nil {
		t.Fatal("resume succeeded without Docker")
	}
	after, err := store.Session(t.Context(), session.ID)
	if err != nil || after.Storage != session.Storage || after.Workspace != session.Workspace {
		t.Fatalf("resume changed retained environment: %+v, %v", after, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatalf("failed resume changed workspace files: %q, %v", data, err)
	}
}
