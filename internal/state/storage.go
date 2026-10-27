package state

import "context"

func (s *Store) Storage(ctx context.Context, key string) (Storage, error) {
	var storage Storage
	err := s.db.QueryRowContext(ctx, "SELECT storage_key, state_volume, provisioned FROM sessions WHERE storage_key = ?", key).
		Scan(&storage.Key, &storage.StateVolume, &storage.Provisioned)
	return storage, err
}

func (s *Store) ProvisionSessionStorage(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET provisioned = 1 WHERE storage_key = ?", key)
	return err
}
