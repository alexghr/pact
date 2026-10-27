package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/session"
	"github.com/alexghr/pact/internal/state"
)

func TestSessionDiscoveryFromUnrelatedDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	t.Chdir(t.TempDir())
	store, err := session.OpenStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := failedSession(t, store, "Investigate deployments")
	second := failedSession(t, store, "Review migrations")
	if err := store.FinishExecution(t.Context(), second.ExecutionID, state.StartupFailed, 1, "runtime unavailable"); err != nil {
		t.Fatal(err)
	}
	list := func(args ...string) []state.Session {
		t.Helper()
		var output bytes.Buffer
		if err := manageSessions(append([]string{"list", "--json"}, args...), &output); err != nil {
			t.Fatal(err)
		}
		var sessions []state.Session
		if err := json.Unmarshal(output.Bytes(), &sessions); err != nil {
			t.Fatal(err)
		}
		return sessions
	}
	for _, sessions := range [][]state.Session{list(), list("--all")} {
		if len(sessions) != 2 || sessions[0].ID != second.ID || sessions[1].ID != first.ID || sessions[0].Title != "Review migrations" || sessions[0].Status != "startupFailed" {
			t.Fatalf("unrelated directory hid chores or failed start: %+v", sessions)
		}
	}
	alias := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(first.Workspace, alias); err != nil {
		t.Fatal(err)
	}
	if filtered := list("--workspace", alias); len(filtered) != 1 || filtered[0].ID != first.ID {
		t.Fatalf("explicit filter selected other workspaces: %+v", filtered)
	}
	var output bytes.Buffer
	if err := manageSessions([]string{"list"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, session := range list() {
		for _, value := range []string{fmt.Sprint(session.ID), session.Title, session.Status} {
			if !strings.Contains(output.String(), value) {
				t.Fatalf("human listing lacks %q: %s", value, output.String())
			}
		}
	}
	output.Reset()
	if err := manageSessions([]string{"show", fmt.Sprint(first.ID)}, &output); err != nil || !strings.Contains(output.String(), first.Workspace) {
		t.Fatalf("inspection lost workspace: %s, %v", output.String(), err)
	}
}

func TestWorkspaceRemovalRequiresExplicitRequest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := session.OpenStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	launch := failedSession(t, store, "")
	file := filepath.Join(launch.Workspace, "unfinished-work")
	if err := os.WriteFile(file, []byte("valuable"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"remove", fmt.Sprint(launch.ID)}
	if err := manageWorkspaces(args, &output); err != nil || !strings.Contains(output.String(), launch.Workspace) {
		t.Fatalf("workspace preview: %s, %v", output.String(), err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "valuable" {
		t.Fatalf("preview removed data: %q, %v", data, err)
	}
	if err := manageWorkspaces(append(args, "--yes"), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("explicit request did not remove workspace files: %v", err)
	}
	if session, err := store.Session(t.Context(), launch.ID); err != nil || session.WorkspaceStatus != "removed" {
		t.Fatalf("explicit removal lost its retained record: %+v, %v", session, err)
	}
}

func failedSession(t *testing.T, store *state.Store, title string) state.Session {
	t.Helper()
	// Fail against a real unreachable daemon after the workspace is recorded.
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	nix := session.Nix{Container: "pact-test-nix", Volume: "pact-test-nix-volume"}
	if _, err := session.New(t.Context(), store, nix, session.Options{Title: title}); err == nil {
		t.Fatal("session unexpectedly started without Docker")
	}
	records, err := store.Sessions(t.Context(), "")
	if err != nil || len(records) == 0 {
		t.Fatalf("failed start lost session: %+v, %v", records, err)
	}
	return records[0]
}
