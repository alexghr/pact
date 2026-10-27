package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

func saveThreads(ctx context.Context, store *state.Store, sessionID int64, threads []harness.Thread) error {
	saved := make([]state.Thread, 0, len(threads))
	for _, thread := range threads {
		saved = append(saved, state.Thread{
			ID: thread.ID, SessionID: sessionID, ParentID: thread.ParentID, Title: thread.Title,
			CreatedAt: thread.CreatedAt, LastUsed: thread.LastUsed, Archived: thread.Archived,
			Internal: thread.Internal, Missing: thread.Missing,
		})
	}
	return store.RefreshThreads(ctx, sessionID, saved)
}

// The bridge binds events to this launch; the container cannot choose another
// session ID. A complete snapshot is applied transactionally, including empty lists.
func sessionEvents(store *state.Store, sessionID int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		var event harness.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			http.Error(w, "invalid harness event", http.StatusBadRequest)
			return
		}
		if event.Type == "binding" {
			if err := store.BindBackendRef(r.Context(), sessionID, event.BackendRef); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if event.Type != "threads" {
			http.Error(w, "unknown harness event", http.StatusBadRequest)
			return
		}
		if err := saveThreads(r.Context(), store, sessionID, event.Threads); err != nil {
			http.Error(w, fmt.Sprintf("save conversations: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
