package state

import "testing"

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
