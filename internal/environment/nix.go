package environment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

// startNixDaemon reuses or creates the shared daemon on an existing volume.
// The container remains after errors and shutdown.
func startNixDaemon(ctx context.Context, storage state.SharedStorage, image string) error {
	name := storage.Volume
	info, err := docker.Inspect(ctx, "container", name)
	if err != nil {
		return err
	}
	if info == nil {
		_, err := docker.CreateContainer(ctx, docker.ContainerConfig{
			Name: name, Image: image, Restart: "unless-stopped", Labels: map[string]string{ownershipLabel: name},
			// Docker initializes an empty volume from upstream Nix's own store.
			Mounts: []docker.Mount{{Type: "volume", Source: name, Target: "/nix"}},
			Env: []string{"PACT_STORAGE_ID=" + name, "PACT_STORAGE_PROVISIONED=" + strconv.FormatBool(storage.Provisioned),
				"HOST_UID=" + strconv.Itoa(os.Getuid()), "HOST_GID=" + strconv.Itoa(os.Getgid())},
		})
		if err != nil {
			return err
		}
		info, err = docker.Inspect(ctx, "container", name)
		if err != nil {
			return err
		}
	}
	if info == nil {
		return fmt.Errorf("Nix daemon %s disappeared before start", name)
	}
	if info.Labels[ownershipLabel] != name {
		return fmt.Errorf("refusing unowned Docker container %s", name)
	}
	if info.Config.Image != image {
		return fmt.Errorf("incompatible Nix daemon %s: running image %s, this Pact requires %s; use the matching Pact version or stop all sessions and upgrade the daemon before retrying", name, info.Config.Image, image)
	}
	if err := docker.StartContainer(ctx, name); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		if _, err := docker.ExecOutput(readyCtx, name, "nix", "--store", "daemon", "store", "info"); err == nil {
			return nil
		}
		info, err := docker.Inspect(readyCtx, "container", name)
		if err != nil || info == nil || !info.State.Running {
			var logs strings.Builder
			_ = docker.Logs(ctx, name, 20, docker.Streams{Stdout: &logs, Stderr: &logs})
			return errors.Join(fmt.Errorf("Nix daemon failed to start: %s", logs.String()), err)
		}
		select {
		case <-readyCtx.Done():
			return context.Cause(readyCtx)
		case <-time.After(100 * time.Millisecond):
		}
	}
}
