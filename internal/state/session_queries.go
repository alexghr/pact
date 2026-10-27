package state

import (
	"context"
	"encoding/json"
	"fmt"
)

// Session joins a retained environment with its most recent execution.
type Session struct {
	ID               int64          `json:"id"`
	Title            string         `json:"title"`
	UpdatedAt        int64          `json:"updatedAt"`
	Workspace        string         `json:"workspace"`
	WorkspaceManaged bool           `json:"workspaceManaged"`
	WorkspaceStatus  string         `json:"workspaceStatus"`
	BackendRef       string         `json:"backendRef,omitempty"`
	Backend          string         `json:"backend"`
	CreatedAt        int64          `json:"createdAt"`
	Status           string         `json:"status"`
	Error            string         `json:"error,omitempty"`
	Settings         LaunchSettings `json:"settings"`
	Environment      Environment    `json:"-"`
}

const sessionQuery = `
    SELECT s.id, COALESCE(NULLIF(s.title, ''), 'Untitled session'),
           MAX(s.created_at, COALESCE(x.finished_at, x.started_at, x.created_at, 0)) AS updated_at,
	       w.path, w.managed, w.status, s.backend, s.backend_ref, s.created_at,
	       COALESCE(x.status, ''), COALESCE(x.error, ''), COALESCE(x.settings, '{}'),
	       e.id, e.workspace_id, e.state_volume, e.nix_volume, e.provisioned
	FROM sessions s
	JOIN environments e ON e.id = s.environment_id
	JOIN workspaces w ON w.id = e.workspace_id
	LEFT JOIN executions x ON x.id = (SELECT MAX(id) FROM executions WHERE session_id = s.id)`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var session Session
	var settings string
	err := row.Scan(&session.ID, &session.Title, &session.UpdatedAt, &session.Workspace, &session.WorkspaceManaged, &session.WorkspaceStatus, &session.Backend, &session.BackendRef, &session.CreatedAt,
		&session.Status, &session.Error, &settings,
		&session.Environment.ID, &session.Environment.WorkspaceID, &session.Environment.StateVolume,
		&session.Environment.NixVolume, &session.Environment.Provisioned)
	if err != nil {
		return session, err
	}
	err = json.Unmarshal([]byte(settings), &session.Settings)
	return session, err
}

func (s *Store) Session(ctx context.Context, id int64) (Session, error) {
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+" WHERE s.id = ?", id))
	if err != nil {
		return Session{}, fmt.Errorf("read session %d: %w", id, err)
	}
	return session, nil
}

// Sessions filters by the canonical host workspace; empty means all workspaces.
func (s *Store) Sessions(ctx context.Context, workspace string) ([]Session, error) {
	query := sessionQuery
	var args []any
	if workspace != "" {
		query += " WHERE w.path = ?"
		args = append(args, workspace)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY updated_at DESC, s.id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []Session{}
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}
