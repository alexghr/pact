package environment

import (
	"context"
	"fmt"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

// Collect only reclaims unreferenced, catalogue-owned private resources.
// Removing roots is separate from Nix GC; the shared store is always retained.
func Collect(ctx context.Context, store *state.Store) ([]string, error) {
	environments, err := store.StaleEnvironments(ctx)
	if err != nil || len(environments) == 0 {
		return nil, err
	}
	storage, err := store.SharedStorage(ctx)
	if err != nil {
		return nil, err
	}
	if storage.Provisioned {
		if err := owned(ctx, "volume", storage.Volume, storage.Volume); err != nil {
			return nil, err
		}
		if err := owned(ctx, "container", storage.Volume, storage.Volume); err != nil {
			return nil, fmt.Errorf("restore/start the recorded Nix daemon before cleanup: %w", err)
		}
		if _, err := docker.ExecOutput(ctx, storage.Volume, "test", "-d", Roots); err != nil {
			return nil, err
		}
	}
	var removed []string
	for _, env := range environments {
		if env.NixVolume != storage.Volume || env.StateVolume == storage.Volume {
			return removed, fmt.Errorf("environment %s has inconsistent storage references", env.ID)
		}
		found, err := exists(ctx, "volume", env.StateVolume)
		if err != nil {
			return removed, err
		}
		if found {
			if err := owned(ctx, "volume", env.StateVolume, env.ID); err != nil {
				return removed, err
			}
			// Even an unrecorded/stopped Docker container can still need this state.
			users, err := docker.ContainersUsingVolume(ctx, env.StateVolume)
			if err != nil {
				return removed, err
			}
			if len(users) != 0 {
				continue
			}
			if err := docker.RemoveVolume(ctx, env.StateVolume); err != nil {
				return removed, err
			}
		}
		// Provisioning creates environment roots only after shared provisioning is
		// recorded. A partial setup before that point has no roots to remove.
		if storage.Provisioned {
			if _, err := docker.ExecOutput(ctx, storage.Volume, "rm", "-rf", "--", Roots+env.ID); err != nil {
				return removed, err
			}
		}
		if err := store.RemoveEnvironment(ctx, env.ID); err != nil {
			return removed, err
		}
		removed = append(removed, env.ID)
	}
	return removed, nil
}
