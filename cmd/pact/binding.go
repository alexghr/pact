package main

import (
	"encoding/json"
	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
	"net/http"
)

// The handler is bound to the host-selected session; native navigation cannot
// redirect that session or choose a different catalogue entry.
func sessionBinding(store *state.Store, id int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var binding harness.Binding
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&binding); err != nil {
			http.Error(w, "invalid binding", http.StatusBadRequest)
			return
		}
		if err := store.BindBackendRef(r.Context(), id, binding.BackendRef); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
