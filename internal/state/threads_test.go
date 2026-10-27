package state

import (
	"testing"
)

func TestThreadObservationKeepsSessionAndExecutionOwnership(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	options := LaunchOptions{Workspace: t.TempDir(), Owner: "operator", Backend: "codex"}
	first, err := store.CreateLaunch(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateLaunch(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	const id = "opaque-thread-id"
	original := Thread{SessionID: second.SessionID, ID: id, Title: "Other session", CreatedAt: 10, LastUsed: 30}
	if err := store.RecordThread(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveThread(t.Context(), first.ExecutionID, id, 20); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveThread(t.Context(), first.ExecutionID, id, 15); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveThread(t.Context(), first.ExecutionID, "another-thread", 40); err == nil {
		t.Fatal("an observation redirected an execution to another thread")
	}
	threads, err := store.Threads(t.Context(), first.SessionID)
	if err != nil || len(threads) != 1 || threads[0].ID != id || threads[0].LastUsed != 20 {
		t.Fatalf("first session lost its observation: %v, %v", threads, err)
	}
	threads, err = store.Threads(t.Context(), second.SessionID)
	if err != nil || len(threads) != 1 || threads[0] != original {
		t.Fatalf("another session was changed: %v, %v", threads, err)
	}
}

func TestBackendBindingSurvivesNativeNavigation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	launch, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"original", "original", "native-new"} {
		if err := store.BindBackendRef(t.Context(), launch.SessionID, ref); err != nil {
			t.Fatal(err)
		}
	}
	reader := openStore(t)
	saved, err := reader.Session(t.Context(), launch.SessionID)
	if err != nil || saved.BackendRef != "original" {
		t.Fatalf("native navigation redirected session: %+v, %v", saved, err)
	}
}
