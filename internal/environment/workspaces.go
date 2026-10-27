package environment

import (
	"context"

	"github.com/alexghr/pact/internal/state"
)

// RemoveWorkspace retains the catalogue and private state, including environments
// referenced by other sessions. Only the explicitly selected workspace is lost.
func RemoveWorkspace(ctx context.Context, store *state.Store, sessionID int64) error {
	session, err := store.Session(ctx, sessionID)
	if err != nil {
		return err
	}
	return store.RemoveWorkspace(ctx, session.Storage.WorkspaceID)
}
