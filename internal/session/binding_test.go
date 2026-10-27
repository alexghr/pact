package session

import (
	"github.com/alexghr/pact/internal/state"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionBindingOnlyChangesBoundSession(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := OpenStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateLaunch(t.Context(), state.LaunchOptions{Workspace: t.TempDir(), Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(sessionBinding(store, first.SessionID))
	defer server.Close()
	for _, body := range []string{`{"backendRef":"initial"}`, `{"backendRef":"native-new"}`} {
		response, err := server.Client().Post(server.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatal(response.Status)
		}
	}
	firstSaved, err := store.Session(t.Context(), first.SessionID)
	if err != nil || firstSaved.BackendRef != "initial" {
		t.Fatalf("initial binding changed: %+v, %v", firstSaved, err)
	}
	otherSaved, err := store.Session(t.Context(), other.SessionID)
	if err != nil || otherSaved.BackendRef != "" {
		t.Fatalf("another session changed: %+v, %v", otherSaved, err)
	}
}
