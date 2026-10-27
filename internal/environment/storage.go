// Package environment provisions catalogue-owned Docker storage and Nix tools.
package environment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/sidecar"
	"github.com/alexghr/pact/internal/state"
)

const ownershipLabel = "me.alexghr.pact.storage"
const Roots = "/nix/var/nix/gcroots/environments/"

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

// Prepare reuses recorded resource identities.
// Resources remain after any error so an interrupted provision can be retried.
func Prepare(ctx context.Context, store *state.Store, env state.Environment) error {
	env, err := store.Environment(ctx, env.ID)
	if err != nil {
		return err
	}
	storage, err := store.SharedStorage(ctx)
	if err != nil {
		return err
	}
	if env.NixVolume != storage.Volume {
		return errors.New("environment does not belong to the managed shared store")
	}
	daemonImage, err := container.BuildDaemonImage(ctx)
	if err != nil {
		return err
	}
	if err := volume(ctx, env.StateVolume, env.ID, env.Provisioned); err != nil {
		return err
	}
	sharedExists, err := exists(ctx, "volume", storage.Volume)
	if err != nil {
		return err
	}
	if !sharedExists && storage.Provisioned {
		if err := store.ResetStorage(ctx); err != nil {
			return err
		}
		storage.Provisioned = false
		fmt.Fprintln(os.Stderr, "Shared Nix volume is missing; recreating the package cache. Project dependencies will need rebuilding.")
	}
	if err := volume(ctx, storage.Volume, storage.Volume, false); err != nil {
		return err
	}
	if err := sidecar.StartNixSidecar(ctx, sidecar.NixConfig{
		Volume: storage.Volume, Image: daemonImage, Provisioned: storage.Provisioned,
		Labels: map[string]string{ownershipLabel: storage.Volume},
	}); err != nil {
		return err
	}
	if err := store.ProvisionStorage(ctx); err != nil {
		return err
	}
	if err := realiseTools(ctx, storage.Volume); err != nil {
		return err
	}
	// A direct directory mount at the same absolute path in both namespaces
	// makes environment roots resolvable even when its agent is stopped.
	// Profile generations retain tool versions used before a live update.
	if _, err := docker.ExecOutput(ctx, storage.Volume, "sh", "-ec",
		`mkdir -p "$1/toolsets"
chown "$2:$3" "$1"
tools=$(readlink -f /nix/var/nix/profiles/pact-tools)
ln -sfn "$tools" "$1/toolsets/${tools##*/}"`, "sh", Roots+env.ID, strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())); err != nil {
		return err
	}
	return store.ProvisionEnvironment(ctx, env.ID)
}

// ConfigureContainer only grants writes to this environment's roots, never the store/DB.
func ConfigureContainer(env state.Environment, config *docker.ContainerConfig) {
	config.Mounts = append(config.Mounts,
		docker.Mount{Type: "volume", Source: env.NixVolume, Target: "/nix", ReadOnly: true, NoCopy: true},
		docker.Mount{Type: "volume", Source: env.NixVolume, Subpath: "var/nix/gcroots/environments/" + env.ID, Target: Roots + env.ID, NoCopy: true},
	)
	config.Env = append(config.Env, "NIX_REMOTE=daemon", "PACT_NIX_ROOTS="+Roots+env.ID)
}
