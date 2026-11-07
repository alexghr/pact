package session

import (
	"context"
	"fmt"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

const ownershipLabel = "me.alexghr.pact.storage"

// prepareStorage creates only persistent resources; startup failures retain them.
func prepareStorage(ctx context.Context, store *state.Store, storage state.Storage) error {
	current, err := store.Storage(ctx, storage.Key)
	if err != nil {
		return err
	}
	if err := volume(ctx, current.StateVolume, current.Key, current.Provisioned); err != nil {
		return err
	}
	return store.ProvisionSessionStorage(ctx, current.Key)
}

func volume(ctx context.Context, name, owner string, provisioned bool) error {
	found, err := docker.Exists(ctx, "volume", name)
	if err != nil {
		return err
	}
	if !found {
		if provisioned {
			return fmt.Errorf("established volume %s is missing; restore it from backup before retrying", name)
		}
		if err := docker.CreateVolume(ctx, name, map[string]string{ownershipLabel: owner}); err != nil {
			return err
		}
	}
	return owned(ctx, "volume", name, owner)
}

func owned(ctx context.Context, kind, name, owner string) error {
	info, err := docker.Inspect(ctx, kind, name)
	if err != nil {
		return err
	}
	if info.Labels[ownershipLabel] != owner {
		return fmt.Errorf("refusing unowned Docker %s %s", kind, name)
	}
	return nil
}

// configureStorage keeps store writes behind the shared daemon.
func configureStorage(nix Nix, config *docker.ContainerConfig) {
	config.Mounts = append(config.Mounts,
		docker.Mount{Type: "volume", Source: nix.Volume, Target: "/nix", ReadOnly: true, NoCopy: true},
	)
	config.Env = append(config.Env, "NIX_REMOTE=daemon")
}
