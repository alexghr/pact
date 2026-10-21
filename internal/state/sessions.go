package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LaunchSettings records non-secret execution choices, not conversation content.
type LaunchSettings struct {
	Model    string `json:"model"`
	Effort   string `json:"effort,omitempty"`
	Prompted bool   `json:"prompted"`
	DinD     bool   `json:"dind"`
	HostNet  bool   `json:"hostNet,omitempty"`
}

type Environment struct {
	ID          string
	WorkspaceID int64
	StateVolume string
	NixVolume   string
	Provisioned bool
}

type LaunchOptions struct {
	Workspace string
	Owner     string
	Backend   string
	Settings  LaunchSettings
}

type Launch struct {
	SessionID   int64
	ExecutionID int64
	Workspace   string
	Environment Environment
}

type ExecutionStatus string

const (
	Succeeded     ExecutionStatus = "succeeded"
	Failed        ExecutionStatus = "failed"
	StartupFailed ExecutionStatus = "startupFailed"
	Interrupted   ExecutionStatus = "interrupted"
	Uncertain     ExecutionStatus = "uncertain"
)

// CreateLaunch registers the workspace and allocates the launch records together.
func (s *Store) CreateLaunch(ctx context.Context, options LaunchOptions) (Launch, error) {
	workspace, err := s.resolveWorkspace(options.Workspace)
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
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO workspaces(path, owner, created_at) VALUES(?, ?, ?) ON CONFLICT(path) DO NOTHING",
		workspace, options.Owner, now,
	); err != nil {
		return Launch{}, fmt.Errorf("register workspace: %w", err)
	}
	launch := Launch{
		Workspace:   workspace,
		Environment: Environment{ID: rand.Text()},
	}
	volume := "pact-nix-" + strings.ToLower(rand.Text())
	if _, err := tx.ExecContext(ctx, "INSERT INTO shared_storage(singleton, volume) VALUES(1, ?) ON CONFLICT DO NOTHING", volume); err != nil {
		return Launch{}, fmt.Errorf("record shared storage intent: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT volume FROM shared_storage WHERE singleton = 1").Scan(&launch.Environment.NixVolume); err != nil {
		return Launch{}, err
	}
	launch.Environment.StateVolume = "pact-state-" + strings.ToLower(launch.Environment.ID)
	if err := tx.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE path = ?", workspace).Scan(&launch.Environment.WorkspaceID); err != nil {
		return Launch{}, fmt.Errorf("find workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO environments(id, workspace_id, state_volume, nix_volume, created_at) VALUES(?, ?, ?, ?, ?)",
		launch.Environment.ID, launch.Environment.WorkspaceID, launch.Environment.StateVolume, launch.Environment.NixVolume, now,
	); err != nil {
		return Launch{}, fmt.Errorf("record environment: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO sessions(environment_id, owner, backend, created_at) VALUES(?, ?, ?, ?)",
		launch.Environment.ID, options.Owner, options.Backend, now,
	)
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
	return launch, nil
}

// StartExecution records the foreground runtime process, not a model turn.
func (s *Store) StartExecution(ctx context.Context, id int64, threadID string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE executions SET status = 'running', started_at = ?, thread_id = ? WHERE id = ?",
		time.Now().UnixMilli(), threadID, id,
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
