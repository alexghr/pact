package state

import (
	"context"
	"fmt"
)

type SharedStorage struct {
	Volume      string
	Provisioned bool
}

func (s *Store) Environment(ctx context.Context, id string) (Environment, error) {
	var env Environment
	err := s.db.QueryRowContext(ctx, "SELECT id, workspace_id, state_volume, nix_volume, provisioned FROM environments WHERE id = ?", id).
		Scan(&env.ID, &env.WorkspaceID, &env.StateVolume, &env.NixVolume, &env.Provisioned)
	return env, err
}

func (s *Store) SessionEnvironment(ctx context.Context, sessionID int64) (Environment, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, "SELECT environment_id FROM sessions WHERE id = ?", sessionID).Scan(&id); err != nil {
		return Environment{}, fmt.Errorf("find session %d: %w", sessionID, err)
	}
	return s.Environment(ctx, id)
}

func (s *Store) SharedStorage(ctx context.Context) (SharedStorage, error) {
	var storage SharedStorage
	err := s.db.QueryRowContext(ctx, "SELECT volume, provisioned FROM shared_storage WHERE singleton = 1").Scan(&storage.Volume, &storage.Provisioned)
	return storage, err
}

func (s *Store) ProvisionStorage(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE shared_storage SET provisioned = 1 WHERE singleton = 1")
	return err
}

// ResetStorage records intent to recreate a missing shared package cache.
// Private agent state retains its independent provisioning flags.
func (s *Store) ResetStorage(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE shared_storage SET provisioned = 0 WHERE singleton = 1")
	return err
}

func (s *Store) ProvisionEnvironment(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE environments SET provisioned = 1 WHERE id = ?", id)
	return err
}

// StaleEnvironments excludes retained sessions, unfinished executions (through
// their session), and volumes referenced by another environment.
func (s *Store) StaleEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workspace_id, state_volume, nix_volume, provisioned
		FROM environments e WHERE NOT EXISTS (SELECT 1 FROM sessions WHERE environment_id = e.id)
		AND NOT EXISTS (SELECT 1 FROM environments other WHERE other.id <> e.id AND (other.state_volume = e.state_volume OR other.nix_volume = e.state_volume))
		ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var environments []Environment
	for rows.Next() {
		var env Environment
		if err := rows.Scan(&env.ID, &env.WorkspaceID, &env.StateVolume, &env.NixVolume, &env.Provisioned); err != nil {
			return nil, err
		}
		environments = append(environments, env)
	}
	return environments, rows.Err()
}

func (s *Store) RemoveEnvironment(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM environments WHERE id = ? AND NOT EXISTS (SELECT 1 FROM sessions WHERE environment_id = ?)", id, id)
	return err
}
