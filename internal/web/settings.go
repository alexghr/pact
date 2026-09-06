package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetPreferences(r.Context())
	if err != nil {
		s.serverError(w, r, "read preferences", err)
		return
	}
	models, err := s.store.ListModels(r.Context())
	if err != nil {
		s.serverError(w, r, "list models", err)
		return
	}
	s.render(w, r, "settings", pageData{Title: "Settings", Preferences: p, Models: models, ImageProfiles: builtinImageProfileNames(), EffortLevels: supportedEffortLevels()})
}

func (s *Server) changeSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var err error
	switch r.PathValue("action") {
	case "preferences":
		id, parseErr := strconv.ParseInt(r.FormValue("default_model_id"), 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid default model", http.StatusBadRequest)
			return
		}
		err = harness.SavePreferences(r.Context(), s.store, state.Preferences{DefaultModelID: id, DefaultImageProfile: r.FormValue("default_image_profile")})
	case "model", "delete-model":
		var id int64
		if value := r.FormValue("id"); value != "" {
			id, err = strconv.ParseInt(value, 10, 64)
			if err != nil || id < 1 {
				http.Error(w, "invalid model", http.StatusBadRequest)
				return
			}
		}
		if r.PathValue("action") == "delete-model" {
			err = s.store.DeleteModel(r.Context(), id)
		} else {
			err = s.store.SaveModel(r.Context(), state.Model{ID: id, Model: r.FormValue("model"), Name: r.FormValue("name"), DefaultEffort: r.FormValue("default_effort")})
		}
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, state.ErrInvalidSettings) || errors.Is(err, state.ErrModelNotFound) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, "save settings", err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
