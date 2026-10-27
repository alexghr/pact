package state

import "context"

type Workspace struct {
	ID       int64
	Path     string
	Owner    string
	Managed  bool
	Status   string
	Identity string
}

func (s *Store) Workspace(ctx context.Context, id int64) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRowContext(ctx, "SELECT id, path, owner, managed, status, identity FROM workspaces WHERE id = ?", id).
		Scan(&w.ID, &w.Path, &w.Owner, &w.Managed, &w.Status, &w.Identity)
	return w, err
}

// Workspaces returns recorded directories without inspecting the filesystem.
func (s *Store) Workspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, path, owner, managed, status, identity FROM workspaces ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Path, &w.Owner, &w.Managed, &w.Status, &w.Identity); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (s *Store) UpdateWorkspace(ctx context.Context, w Workspace) error {
	result, err := s.db.ExecContext(ctx, "UPDATE workspaces SET status = ?, identity = ? WHERE id = ?", w.Status, w.Identity, w.ID)
	return recordUpdate(result, err)
}

func (s *Store) WorkspaceByPath(ctx context.Context, path string) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRowContext(ctx, "SELECT id, path, owner, managed, status, identity FROM workspaces WHERE path = ?", path).
		Scan(&w.ID, &w.Path, &w.Owner, &w.Managed, &w.Status, &w.Identity)
	return w, err
}
