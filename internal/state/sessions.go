package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LaunchSettings records non-secret execution choices, not conversation content.
type Capabilities struct {
	DIND        bool `json:"dind"`
	HostNetwork bool `json:"hostNetwork"`
}

type LaunchSettings struct {
	Model        string       `json:"model"`
	Effort       string       `json:"effort,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

type Storage struct {
	Key         string
	WorkspaceID int64
	StateVolume string
	NixVolume   string
	Provisioned bool
}

type LaunchOptions struct {
	Workspace string
	Title     string
	Owner     string
	Backend   string
	Settings  LaunchSettings
}

type Launch struct {
	SessionID   int64
	ExecutionID int64
	Workspace   string
	Storage     Storage
}

type ExecutionStatus string

const (
	Succeeded     ExecutionStatus = "succeeded"
	Failed        ExecutionStatus = "failed"
	StartupFailed ExecutionStatus = "startupFailed"
	Interrupted   ExecutionStatus = "interrupted"
)

// CreateLaunch registers the workspace and allocates the launch records together.
// An empty Workspace requests a fresh managed directory.
func (s *Store) CreateLaunch(ctx context.Context, options LaunchOptions) (Launch, error) {
	managed := options.Workspace == ""
	var workspace string
	var err error
	if managed {
		workspace, err = s.newWorkspacePath()
	} else {
		workspace, err = s.resolveWorkspace(options.Workspace)
	}
	if err != nil {
		return Launch{}, err
	}
	settings, err := json.Marshal(options.Settings)
	if err != nil {
		return Launch{}, fmt.Errorf("encode launch settings: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Launch{}, fmt.Errorf("begin launch transaction: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	status := "ready"
	if managed {
		status = "allocating"
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO workspaces(path, owner, created_at, managed, status) VALUES(?, ?, ?, ?, ?) ON CONFLICT(path) DO NOTHING",
		workspace, options.Owner, now, managed, status,
	); err != nil {
		return Launch{}, fmt.Errorf("register workspace: %w", err)
	}
	launch := Launch{
		Workspace: workspace,
		Storage:   Storage{Key: rand.Text()},
	}
	volume := "pact-nix-" + strings.ToLower(rand.Text())
	if _, err := tx.ExecContext(ctx, "INSERT INTO shared_storage(singleton, volume) VALUES(1, ?) ON CONFLICT DO NOTHING", volume); err != nil {
		return Launch{}, fmt.Errorf("record shared storage intent: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT volume FROM shared_storage WHERE singleton = 1").Scan(&launch.Storage.NixVolume); err != nil {
		return Launch{}, err
	}
	launch.Storage.StateVolume = "pact-state-" + strings.ToLower(launch.Storage.Key)
	if err := tx.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE path = ?", workspace).Scan(&launch.Storage.WorkspaceID); err != nil {
		return Launch{}, fmt.Errorf("find workspace: %w", err)
	}
	if !managed {
		var w Workspace
		if err := tx.QueryRowContext(ctx, "SELECT path, managed, status, identity FROM workspaces WHERE id = ?", launch.Storage.WorkspaceID).
			Scan(&w.Path, &w.Managed, &w.Status, &w.Identity); err != nil {
			return Launch{}, err
		}
		if err := s.checkWorkspace(w); err != nil {
			return Launch{}, err
		}
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO sessions(workspace_id, storage_key, state_volume, nix_volume, owner, backend, created_at, title) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		launch.Storage.WorkspaceID, launch.Storage.Key, launch.Storage.StateVolume, launch.Storage.NixVolume, options.Owner, options.Backend, now, options.Title)
	if err != nil {
		return Launch{}, fmt.Errorf("record session: %w", err)
	}
	launch.SessionID, err = result.LastInsertId()
	if err != nil {
		return Launch{}, fmt.Errorf("read session ID: %w", err)
	}
	result, err = tx.ExecContext(ctx,
		"INSERT INTO executions(session_id, settings, status, created_at) VALUES(?, ?, 'starting', ?)",
		launch.SessionID, string(settings), now,
	)
	if err != nil {
		return Launch{}, fmt.Errorf("record execution: %w", err)
	}
	launch.ExecutionID, err = result.LastInsertId()
	if err != nil {
		return Launch{}, fmt.Errorf("read execution ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Launch{}, fmt.Errorf("commit launch: %w", err)
	}
	if managed {
		if err := s.allocateWorkspace(ctx, launch); err != nil {
			message := fmt.Sprintf("session %d workspace allocation at %s needs recovery: %v", launch.SessionID, workspace, err)
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return Launch{}, errors.Join(errors.New(message), s.FinishExecution(finishCtx, launch.ExecutionID, StartupFailed, 1, message))
		}
	}
	return launch, nil
}

// StartExecution records the foreground runtime process, not a model turn.
func (s *Store) StartExecution(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE executions SET status = 'running', started_at = ? WHERE id = ?",
		time.Now().UnixMilli(), id,
	)
	return executionUpdate(result, err)
}

func (s *Store) FinishExecution(ctx context.Context, id int64, status ExecutionStatus, exitCode int, message string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE executions SET status = ?, finished_at = ?, exit_code = ?, error = ? WHERE id = ?",
		status, time.Now().UnixMilli(), exitCode, message, id,
	)
	return executionUpdate(result, err)
}

func executionUpdate(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("update execution: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check execution update: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("update execution: %w", sql.ErrNoRows)
	}
	return nil
}
