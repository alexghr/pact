package state

import "context"

type SharedStorage struct {
	Volume      string
	Provisioned bool
}

func (s *Store) Storage(ctx context.Context, key string) (Storage, error) {
	var storage Storage
	err := s.db.QueryRowContext(ctx, "SELECT storage_key, state_volume, nix_volume, provisioned FROM sessions WHERE storage_key = ?", key).
		Scan(&storage.Key, &storage.StateVolume, &storage.NixVolume, &storage.Provisioned)
	return storage, err
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

func (s *Store) ProvisionSessionStorage(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET provisioned = 1 WHERE storage_key = ?", key)
	return err
}

func (s *Store) EnsureSharedStorage(ctx context.Context, volume string) (SharedStorage, error) {
	if _, err := s.db.ExecContext(ctx, "INSERT INTO shared_storage(singleton, volume) VALUES(1, ?) ON CONFLICT DO NOTHING", volume); err != nil {
		return SharedStorage{}, err
	}
	return s.SharedStorage(ctx)
}
