package environment

import (
	"context"
	"fmt"
	"os"

	"github.com/alexghr/pact/internal/execution"
	"github.com/alexghr/pact/internal/state"
)

// RemoveWorkspace retains the catalogue and private state, including environments
// referenced by other sessions. Only the explicitly selected workspace is lost.
func RemoveWorkspace(ctx context.Context, store *state.Store, sessionID int64) error {
	session, err := store.Session(ctx, sessionID)
	if err != nil {
		return err
	}
	w, err := store.Workspace(ctx, session.Environment.WorkspaceID)
	if err != nil {
		return err
	}
	if !w.Managed || w.Owner != fmt.Sprintf("uid:%d", os.Getuid()) {
		return fmt.Errorf("session %d does not reference a managed workspace owned by this operator", sessionID)
	}
	references, err := store.WorkspaceReferences(ctx, w)
	if err != nil {
		return err
	}
	for _, launch := range references {
		owner := execution.Owner{Store: store, Launch: launch}
		if err := owner.Recover(ctx); err != nil {
			return fmt.Errorf("workspace removal blocked: %w", err)
		}
	}
	return store.RemoveWorkspace(ctx, w.ID)
}
