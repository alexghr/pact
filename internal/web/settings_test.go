package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestSettingsMutations(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "pact.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	server, err := New(ctx, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	post := func(action string, values url.Values, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/settings/"+action, strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s = %d: %s", action, response.Code, response.Body.String())
		}
	}
	post("model", url.Values{"model": {"future-model"}, "name": {"Future"}, "default_effort": {"high"}}, http.StatusSeeOther)
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, m := range models {
		if m.Model == "future-model" {
			id = m.ID
		}
	}
	if id == 0 {
		t.Fatal("model not created")
	}
	idText := strconv.FormatInt(id, 10)
	post("preferences", url.Values{"default_model_id": {idText}, "default_image_profile": {"go"}}, http.StatusSeeOther)
	post("preferences", url.Values{"default_model_id": {idText}, "default_image_profile": {"/host/private"}}, http.StatusBadRequest)
	post("delete-model", url.Values{"id": {idText}}, http.StatusBadRequest)
	post("model", url.Values{"id": {idText}, "model": {"future-model"}, "name": {"Renamed"}, "default_effort": {"max"}}, http.StatusSeeOther)
	p, err := store.GetPreferences(ctx)
	if err != nil || p.DefaultModelID != id || p.DefaultImageProfile != "go" {
		t.Fatalf("preferences = %#v, %v", p, err)
	}
	models, err = store.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.ID == id && (m.Name != "Renamed" || m.DefaultEffort != "max") {
			t.Fatalf("model = %#v", m)
		}
	}
}
