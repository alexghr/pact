package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

func TestSessionEventsPersistOnlyBoundSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	launch, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(sessionEvents(store, launch.SessionID))
	t.Cleanup(server.Close)
	post := func(event harness.Event) int {
		t.Helper()
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	event := harness.Event{Type: "threads", Threads: []harness.Thread{{ID: "native-A", Title: "Saved A", CreatedAt: 1, LastUsed: 2}}}
	if code := post(event); code != http.StatusNoContent {
		t.Fatalf("publish: %d", code)
	}
	threads, err := store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || threads[0].Title != "Saved A" {
		t.Fatalf("saved: %+v, %v", threads, err)
	}
	others, err := store.Threads(t.Context(), other.SessionID)
	if err != nil || len(others) != 0 {
		t.Fatalf("other session changed: %+v, %v", others, err)
	}
	// A rejected snapshot must not erase the last successful discovery.
	event.Threads = append(event.Threads, event.Threads[0])
	if code := post(event); code < 400 {
		t.Fatalf("duplicate snapshot accepted: %d", code)
	}
	threads, err = store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || threads[0].Missing {
		t.Fatalf("rejected snapshot changed catalogue: %+v, %v", threads, err)
	}
	if code := post(harness.Event{Type: "threads", Threads: []harness.Thread{}}); code != http.StatusNoContent {
		t.Fatalf("empty snapshot: %d", code)
	}
	threads, err = store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || !threads[0].Missing {
		t.Fatalf("lost history became empty: %+v, %v", threads, err)
	}
}

func TestSessionDiscoveryFromUnrelatedDirectory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	t.Chdir(t.TempDir())
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Backend: "codex", Title: "Investigate deployments"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Backend: "codex", Title: "Review migrations"})
	if err != nil {
		t.Fatal(err)
	}
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
		if len(sessions) != 2 || sessions[0].ID != second.SessionID || sessions[1].ID != first.SessionID || sessions[0].Title != "Review migrations" || sessions[0].Status != "startupFailed" {
			t.Fatalf("unrelated directory hid chores or failed start: %+v", sessions)
		}
	}
	alias := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(first.Workspace, alias); err != nil {
		t.Fatal(err)
	}
	if filtered := list("--workspace", alias); len(filtered) != 1 || filtered[0].ID != first.SessionID {
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
	if err := manageSessions([]string{"show", fmt.Sprint(first.SessionID)}, &output); err != nil || !strings.Contains(output.String(), first.Workspace) {
		t.Fatalf("inspection lost workspace: %s, %v", output.String(), err)
	}
}

func TestWorkspaceRemovalRequiresExplicitRequest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	launch, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Backend: "codex", Owner: fmt.Sprintf("uid:%d", os.Getuid())})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(launch.Workspace, "unfinished-work")
	if err := os.WriteFile(file, []byte("valuable"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"remove", fmt.Sprint(launch.SessionID)}
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
	if session, err := store.Session(t.Context(), launch.SessionID); err != nil || session.WorkspaceStatus != "removed" {
		t.Fatalf("explicit removal lost its retained record: %+v, %v", session, err)
	}
}
