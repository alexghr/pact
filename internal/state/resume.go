package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ResumeLaunch allocates only a new execution, retaining session and storage.
func (s *Store) ResumeLaunch(ctx context.Context, id int64) (Launch, LaunchSettings, error) {
	session, err := s.Session(ctx, id)
	if err != nil {
		return Launch{}, LaunchSettings{}, err
	}
	w, err := s.Workspace(ctx, session.Storage.WorkspaceID)
	if err != nil {
		return Launch{}, LaunchSettings{}, err
	}
	if err := s.checkWorkspace(w); err != nil {
		return Launch{}, LaunchSettings{}, err
	}
	settings := session.Settings
	encoded, err := json.Marshal(settings)
	if err != nil {
		return Launch{}, LaunchSettings{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO executions(session_id, settings, status, created_at)
		VALUES (?, ?, 'starting', ?)`, id, string(encoded), time.Now().UnixMilli())
	if err != nil {
		return Launch{}, LaunchSettings{}, err
	}
	executionID, err := result.LastInsertId()
	return Launch{SessionID: id, ExecutionID: executionID, Workspace: w.Path, Storage: session.Storage}, settings, err
}

func (s *Store) resolveWorkspace(workspace string) (string, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace must be a directory")
	}
	path, err := filepath.EvalSymlinks(s.path)
	if err != nil {
		return "", fmt.Errorf("resolve catalogue: %w", err)
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return "", err
	}
	if rel == "." || filepath.IsLocal(rel) {
		return "", errors.New("catalogue must be outside the mounted workspace")
	}
	return workspace, nil
}
