package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
