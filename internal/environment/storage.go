// Package environment provisions catalogue-owned Docker storage and a Nix daemon.
package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

const ownershipLabel = "me.alexghr.pact.storage"
const Roots = "/nix/var/nix/gcroots/environments/"

func docker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func exists(ctx context.Context, kind, name string) (bool, error) {
	args := []string{kind, "ls", "--filter", "name=" + name, "--format", "{{.Name}}"}
	if kind == "container" {
		args = []string{"container", "ls", "--all", "--filter", "name=" + name, "--format", "{{.Names}}"}
	}
	output, err := docker(ctx, args...)
	for line := range strings.Lines(output) {
		if strings.TrimSpace(line) == name {
			return true, err
		}
	}
	return false, err
}

func owned(ctx context.Context, kind, name, owner string) error {
	field := ".Labels"
	if kind == "container" {
		field = ".Config.Labels"
	}
	output, err := docker(ctx, kind, "inspect", "--format", "{{json "+field+"}}", name)
	if err != nil {
		return err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(output), &labels); err != nil {
		return err
	}
	if labels[ownershipLabel] != owner {
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
		if _, err := docker(ctx, "volume", "create", "--label", ownershipLabel+"="+owner, name); err != nil {
			return err
		}
	}
	return owned(ctx, "volume", name, owner)
}

// Prepare reuses recorded resource identities. Caller holds Store.LockStorage.
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
	found, err := exists(ctx, "container", storage.Volume)
	if err != nil {
		return err
	}
	if !found {
		_, err = docker(ctx, "create", "--name", storage.Volume, "--restart", "unless-stopped",
			"--label", ownershipLabel+"="+storage.Volume,
			// Docker initializes an empty volume from upstream Nix's own store.
			"--mount", "type=volume,src="+storage.Volume+",dst=/nix",
			"--env", "PACT_STORAGE_ID="+storage.Volume,
			"--env", "PACT_STORAGE_PROVISIONED="+strconv.FormatBool(storage.Provisioned),
			"--env", "HOST_UID="+strconv.Itoa(os.Getuid()), "--env", "HOST_GID="+strconv.Itoa(os.Getgid()),
			daemonImage)
		if err != nil {
			return err
		}
	}
	if err := owned(ctx, "container", storage.Volume, storage.Volume); err != nil {
		return err
	}
	currentImage, err := docker(ctx, "inspect", "--format", "{{.Config.Image}}", storage.Volume)
	if err != nil {
		return err
	}
	if currentImage != daemonImage {
		return fmt.Errorf("incompatible Nix daemon %s: running image %s, this Pact requires %s; use the matching Pact version or stop all sessions and upgrade the daemon before retrying", storage.Volume, currentImage, daemonImage)
	}
	if _, err := docker(ctx, "start", storage.Volume); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		if _, err := docker(readyCtx, "exec", storage.Volume, "nix", "--store", "daemon", "store", "info"); err == nil {
			break
		}
		running, err := docker(readyCtx, "inspect", "--format", "{{.State.Running}}", storage.Volume)
		if err != nil || running != "true" {
			logs, _ := docker(ctx, "logs", "--tail", "20", storage.Volume)
			return errors.Join(fmt.Errorf("Nix daemon failed to start: %s", logs), err)
		}
		select {
		case <-readyCtx.Done():
			return context.Cause(readyCtx)
		case <-time.After(100 * time.Millisecond):
		}
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
	if _, err := docker(ctx, "exec", storage.Volume, "sh", "-ec",
		`mkdir -p "$1/toolsets"
chown "$2:$3" "$1"
tools=$(readlink -f /nix/var/nix/profiles/pact-tools)
ln -sfn "$tools" "$1/toolsets/${tools##*/}"`, "sh", Roots+env.ID, strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())); err != nil {
		return err
	}
	return store.ProvisionEnvironment(ctx, env.ID)
}

// Mounts only grants writes to this environment's roots, never the store/DB.
func Mounts(env state.Environment) []string {
	return []string{
		"--mount", "type=volume,src=" + env.NixVolume + ",dst=/nix,readonly,volume-nocopy",
		"--mount", "type=volume,src=" + env.NixVolume + ",volume-subpath=var/nix/gcroots/environments/" + env.ID + ",dst=" + Roots + env.ID + ",volume-nocopy",
		"--env", "NIX_REMOTE=daemon", "--env", "PACT_NIX_ROOTS=" + Roots + env.ID,
	}
}
