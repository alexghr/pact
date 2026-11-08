package session

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexghr/pact/internal/harness"
)

func TestSessionBindingOnlyChangesBoundSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := OpenStore(t.Context(), filepath.Join(os.Getenv("XDG_STATE_HOME"), "pact", "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := createRecord(t.Context(), store, "", Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other, err := createRecord(t.Context(), store, "", Options{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(sessionBinding(store, first.ID))
	defer server.Close()
	for _, ref := range []string{"initial", "native-new"} {
		if err := harness.PublishBinding(t.Context(), server.URL, ref); err != nil {
			t.Fatal(err)
		}
	}
	firstSaved, err := store.Session(t.Context(), first.ID)
	if err != nil || firstSaved.BackendRef != "initial" {
		t.Fatalf("initial binding changed: %+v, %v", firstSaved, err)
	}
	otherSaved, err := store.Session(t.Context(), other.ID)
	if err != nil || otherSaved.BackendRef != "" {
		t.Fatalf("another session changed: %+v, %v", otherSaved, err)
	}
}
