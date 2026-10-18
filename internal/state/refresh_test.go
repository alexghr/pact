package state

import "testing"

func TestThreadRefresh(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	launch, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateLaunch(t.Context(), LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	const id = "Thread:MixedCase"
	if err := store.ObserveThread(t.Context(), launch.ExecutionID, id, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveThread(t.Context(), other.ExecutionID, id, 200); err != nil {
		t.Fatal(err)
	}
	native := Thread{ID: id, ParentID: "Parent:MixedCase", CreatedAt: 10, LastUsed: 20}
	// Native evidence supersedes the launcher's receipt time, even when older.
	for range 2 {
		if err := store.RefreshThreads(t.Context(), launch.SessionID, []Thread{native}); err != nil {
			t.Fatal(err)
		}
	}
	threads, err := store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || threads[0].LastUsed != 20 || threads[0].ParentID != native.ParentID {
		t.Fatalf("reconciliation: %+v, %v", threads, err)
	}
	if err := store.RefreshThreads(t.Context(), launch.SessionID, []Thread{native, native}); err == nil {
		t.Fatal("accepted ambiguous duplicate history")
	}
	threads, err = store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || threads[0].Missing {
		t.Fatalf("failed reconciliation changed prior snapshot: %+v, %v", threads, err)
	}
	if err := store.RefreshThreads(t.Context(), launch.SessionID, nil); err != nil {
		t.Fatal(err)
	}
	threads, err = store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || !threads[0].Missing {
		t.Fatalf("lost history was forgotten: %+v, %v", threads, err)
	}
	untouched, err := store.Threads(t.Context(), other.SessionID)
	if err != nil || len(untouched) != 1 || untouched[0].Missing || untouched[0].LastUsed != 200 {
		t.Fatalf("reconciliation crossed session boundary: %+v, %v", untouched, err)
	}
	native.Archived = true
	if err := store.RefreshThreads(t.Context(), launch.SessionID, []Thread{native}); err != nil {
		t.Fatal(err)
	}
	threads, err = store.Threads(t.Context(), launch.SessionID)
	if err != nil || len(threads) != 1 || !threads[0].Archived || threads[0].Missing {
		t.Fatalf("restored archive state lost: %+v, %v", threads, err)
	}
}
