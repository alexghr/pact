package session

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

const ownershipLabel = "me.alexghr.pact.storage"
const roots = "/nix/var/nix/gcroots/environments/"

func exists(ctx context.Context, kind, name string) (bool, error) {
	info, err := docker.Inspect(ctx, kind, name)
	return info != nil, err
}

func owned(ctx context.Context, kind, name, owner string) error {
	info, err := docker.Inspect(ctx, kind, name)
	if err != nil {
		return err
	}
	if info == nil || info.Labels[ownershipLabel] != owner {
		return fmt.Errorf("refusing unowned Docker %s %s", kind, name)
	}
	return nil
}

func volume(ctx context.Context, name, owner string, provisioned bool) error {
	found, err := exists(ctx, "volume", name)
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

// prepareStorage creates only persistent resources; startup failures retain them.
func prepareStorage(ctx context.Context, store *state.Store, storage state.Storage, nix Nix) error {
	current, err := store.Storage(ctx, storage.Key)
	if err != nil {
		return err
	}
	if err := volume(ctx, current.StateVolume, current.Key, current.Provisioned); err != nil {
		return err
	}
	if _, err := docker.ExecOutput(ctx, nix.Container, "sh", "-ec",
		`mkdir -p "$1/toolsets"
chown "$2:$3" "$1"
tools=$(readlink -f /nix/var/nix/profiles/pact-tools)
ln -sfn "$tools" "$1/toolsets/${tools##*/}"`, "sh", roots+current.Key, strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())); err != nil {
		return err
	}
	return store.ProvisionSessionStorage(ctx, current.Key)
}

// configureStorage grants writes only to this session's roots, never the store/DB.
func configureStorage(env state.Storage, nix Nix, config *docker.ContainerConfig) {
	config.Mounts = append(config.Mounts,
		docker.Mount{Type: "volume", Source: nix.Volume, Target: "/nix", ReadOnly: true, NoCopy: true},
		docker.Mount{Type: "volume", Source: nix.Volume, Subpath: "var/nix/gcroots/environments/" + env.Key, Target: roots + env.Key, NoCopy: true},
	)
	config.Env = append(config.Env, "NIX_REMOTE=daemon", "PACT_NIX_ROOTS="+roots+env.Key)
}
