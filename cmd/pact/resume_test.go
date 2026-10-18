package main

import (
	"fmt"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestExactResumeDoesNotRedirect(t *testing.T) {
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
	const available = "Thread:MixedCase"
	const target = "01a14ebb-12bb-7be3-b424-03025ccffbb5"
	if err := store.RecordThread(t.Context(), state.Thread{SessionID: launch.SessionID, ID: available}); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseResumeArgs([]string{fmt.Sprintf("%d:%s", launch.SessionID, available)})
	if err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []state.Thread{
		{ID: target, Archived: true},
		{ID: target, Missing: true},
		{ID: target, Internal: true},
	} {
		unavailable.SessionID = launch.SessionID
		if err := store.RecordThread(t.Context(), unavailable); err != nil {
			t.Fatal(err)
		}
		threads, err := store.Threads(t.Context(), launch.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if selected, err := exactThread(threads, target); err == nil || selected.ID != "" {
			t.Fatalf("unavailable target redirected or accepted: %+v, %v", selected, err)
		}
		if selected, err := exactThread(threads, cfg.threadID); err != nil || selected.ID != available {
			t.Fatalf("available explicit target blocked: %+v, %v", selected, err)
		}
	}
	other, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	threads, err := store.Threads(t.Context(), other.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if selected, err := exactThread(threads, available); err == nil || selected.ID != "" {
		t.Fatalf("exact selection crossed session boundary: %+v, %v", selected, err)
	}
}
