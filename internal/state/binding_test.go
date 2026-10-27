package state

import "testing"

func TestBackendBindingSurvivesNativeNavigation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store := openStore(t)
	launch := createSession(t, store, t.TempDir())
	for _, ref := range []string{"original", "original", "native-new"} {
		if err := store.BindBackendRef(t.Context(), launch.ID, ref); err != nil {
			t.Fatal(err)
		}
	}
	reader := openStore(t)
	saved, err := reader.Session(t.Context(), launch.ID)
	if err != nil || saved.BackendRef != "original" {
		t.Fatalf("native navigation redirected session: %+v, %v", saved, err)
	}
}
