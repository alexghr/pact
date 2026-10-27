package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/state"
)

// createRecord records resource identities before allocating a managed workspace.
func createRecord(ctx context.Context, store *state.Store, options Options) (state.Session, error) {
	if options.Backend == "" {
		options.Backend = "codex"
	}
	if options.Backend != "codex" {
		return state.Session{}, fmt.Errorf("unsupported backend %q", options.Backend)
	}
	workspace := state.Workspace{Owner: "uid:" + strconv.Itoa(os.Getuid()), Managed: options.Workspace == "", Status: "ready"}
	var err error
	if workspace.Managed {
		workspace.Path, err = newWorkspacePath(store)
		workspace.Status = "allocating"
	} else {
		workspace.Path, err = resolveWorkspace(store, options.Workspace)
		if err == nil {
			var saved state.Workspace
			saved, err = store.WorkspaceByPath(ctx, workspace.Path)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			} else if err == nil {
				err = checkWorkspace(store, saved)
			}
		}
	}
	if err != nil {
		return state.Session{}, err
	}
	key := rand.Text()
	record, err := store.CreateSession(ctx, workspace, state.Session{
		Title: options.Title, Backend: options.Backend,
		Settings: state.Settings{Model: options.Model, Effort: options.Effort, Capabilities: options.Capabilities},
		Storage:  state.Storage{Key: key, StateVolume: "pact-state-" + strings.ToLower(key)},
	})
	if err != nil {
		return state.Session{}, err
	}
	if workspace.Managed {
		workspace.ID = record.WorkspaceID
		if err := allocateWorkspace(ctx, store, workspace); err != nil {
			failure := fmt.Errorf("session %d workspace allocation at %s needs recovery: %w", record.ID, workspace.Path, err)
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return state.Session{}, errors.Join(failure, store.FinishExecution(finishCtx, record.ExecutionID, state.StartupFailed, 1, failure.Error()))
		}
		record.WorkspaceStatus = "ready"
	}
	return record, nil
}

func restoreRecord(ctx context.Context, store *state.Store, id int64) (state.Session, error) {
	record, err := store.Session(ctx, id)
	if err != nil {
		return state.Session{}, err
	}
	if record.Backend != "codex" {
		return state.Session{}, fmt.Errorf("unsupported backend %q", record.Backend)
	}
	workspace, err := store.Workspace(ctx, record.WorkspaceID)
	if err != nil {
		return state.Session{}, err
	}
	if err := checkWorkspace(store, workspace); err != nil {
		return state.Session{}, err
	}
	record.ExecutionID, err = store.CreateExecution(ctx, id, record.Settings)
	if err != nil {
		return state.Session{}, err
	}
	record.Status, record.Error, record.UpdatedAt = "starting", "", time.Now().UnixMilli()
	return record, nil
}
