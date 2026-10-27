package state

import "context"

type Resource struct {
	Name        string
	Kind        string
	Image       string
	ContainerID string
	ImageID     string
}

// ReserveResource commits creation intent before Docker can create anything.
func (s *Store) ReserveResource(ctx context.Context, executionID int64, resource Resource) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO execution_resources(name, execution_id, kind, image) VALUES (?, ?, ?, ?)",
		resource.Name, executionID, resource.Kind, resource.Image)
	return err
}

func (s *Store) ResourceCreated(ctx context.Context, name, containerID, imageID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE execution_resources SET container_id = ?, image_id = ? WHERE name = ?", containerID, imageID, name)
	return err
}

func (s *Store) ResourceCleaned(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE execution_resources SET cleaned = 1 WHERE name = ?", name)
	return err
}

func (s *Store) Resources(ctx context.Context, executionID int64) ([]Resource, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name, kind, image, container_id, image_id FROM execution_resources WHERE execution_id = ? AND cleaned = 0 ORDER BY rowid DESC", executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resources []Resource
	for rows.Next() {
		var resource Resource
		if err := rows.Scan(&resource.Name, &resource.Kind, &resource.Image, &resource.ContainerID, &resource.ImageID); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

// PendingExecutions includes cleanup failures even when foreground work ended.
func (s *Store) PendingExecutions(ctx context.Context, launch Launch) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT x.id FROM executions x
		JOIN sessions s ON s.id = x.session_id JOIN environments e ON e.id = s.environment_id
		WHERE x.id <> ? AND (e.workspace_id = ? OR e.id = ? OR e.state_volume = ?)
		AND (x.finished_at IS NULL OR EXISTS (SELECT 1 FROM execution_resources r WHERE r.execution_id = x.id AND r.cleaned = 0))
		ORDER BY x.id`, launch.ExecutionID, launch.Environment.WorkspaceID, launch.Environment.ID, launch.Environment.StateVolume)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) UncertainExecution(ctx context.Context, id int64, message string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE executions SET status = 'uncertain', error = ? WHERE id = ?", message, id)
	return err
}
