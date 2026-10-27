package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type Capabilities struct {
	DIND        bool `json:"dind"`
	HostNetwork bool `json:"hostNetwork"`
}

// Settings records non-secret execution choices, not conversation content.
type Settings struct {
	Model        string       `json:"model"`
	Effort       string       `json:"effort,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

type Storage struct {
	Key         string
	StateVolume string
	Provisioned bool
}

type ExecutionStatus string

const (
	Succeeded     ExecutionStatus = "succeeded"
	Failed        ExecutionStatus = "failed"
	StartupFailed ExecutionStatus = "startupFailed"
	Interrupted   ExecutionStatus = "interrupted"
)

// CreateSession records a workspace, session, and initial execution atomically.
// The caller supplies canonical paths and resource identities.
func (s *Store) CreateSession(ctx context.Context, workspace Workspace, session Session) (Session, error) {
	settings, err := json.Marshal(session.Settings)
	if err != nil {
		return Session{}, fmt.Errorf("encode settings: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, fmt.Errorf("begin session transaction: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO workspaces(path, owner, created_at, managed, status, identity) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(path) DO NOTHING",
		workspace.Path, workspace.Owner, now, workspace.Managed, workspace.Status, workspace.Identity,
	); err != nil {
		return Session{}, fmt.Errorf("register workspace: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT id FROM workspaces WHERE path = ?", workspace.Path).Scan(&session.WorkspaceID); err != nil {
		return Session{}, fmt.Errorf("find workspace: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		"INSERT INTO sessions(workspace_id, storage_key, state_volume, owner, backend, created_at, title) VALUES(?, ?, ?, ?, ?, ?, ?)",
		session.WorkspaceID, session.Storage.Key, session.Storage.StateVolume, workspace.Owner, session.Backend, now, session.Title)
	if err != nil {
		return Session{}, fmt.Errorf("record session: %w", err)
	}
	session.ID, err = result.LastInsertId()
	if err != nil {
		return Session{}, fmt.Errorf("read session ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO executions(session_id, settings, status, created_at) VALUES(?, ?, 'starting', ?)",
		session.ID, string(settings), now,
	); err != nil {
		return Session{}, fmt.Errorf("record execution: %w", err)
	}
	session, err = scanSession(tx.QueryRowContext(ctx, sessionQuery+" WHERE s.id = ?", session.ID))
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, fmt.Errorf("commit session: %w", err)
	}
	return session, nil
}

// CreateExecution records another foreground run of an existing session.
func (s *Store) CreateExecution(ctx context.Context, sessionID int64, settings Settings) (int64, error) {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO executions(session_id, settings, status, created_at)
		VALUES (?, ?, 'starting', ?)`, sessionID, string(encoded), time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("record execution: %w", err)
	}
	return result.LastInsertId()
}

// StartExecution records the foreground runtime process, not a model turn.
func (s *Store) StartExecution(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE executions SET status = 'running', started_at = ? WHERE id = ?",
		time.Now().UnixMilli(), id,
	)
	return recordUpdate(result, err)
}

func (s *Store) FinishExecution(ctx context.Context, id int64, status ExecutionStatus, exitCode int, message string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE executions SET status = ?, finished_at = ?, exit_code = ?, error = ? WHERE id = ?",
		status, time.Now().UnixMilli(), exitCode, message, id,
	)
	return recordUpdate(result, err)
}

func recordUpdate(result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("update record: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check record update: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("update record: %w", sql.ErrNoRows)
	}
	return nil
}
