package session

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		res, err := server.Client().Post(server.URL, "application/json", strings.NewReader(`{"backendRef":"`+ref+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNoContent {
			t.Fatalf("binding rejected: %s", res.Status)
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
