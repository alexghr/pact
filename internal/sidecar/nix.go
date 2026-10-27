// Package sidecar starts Pact's shared Nix and private Docker-in-Docker services.
// Callers retain storage provisioning and execution cleanup policy.
package sidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/docker"
)

type NixConfig struct {
	Volume      string
	Image       string
	Provisioned bool
	Labels      map[string]string
}

// StartNixSidecar reuses or creates the shared daemon on an existing volume.
// The container remains after errors and shutdown.
func StartNixSidecar(ctx context.Context, config NixConfig) error {
	name := config.Volume
	info, err := docker.Inspect(ctx, "container", name)
	if err != nil {
		return err
	}
	if info == nil {
		_, err := docker.CreateContainer(ctx, docker.ContainerConfig{
			Name: name, Image: config.Image, Restart: "unless-stopped", Labels: config.Labels,
			// Docker initializes an empty volume from upstream Nix's own store.
			Mounts: []docker.Mount{{Type: "volume", Source: name, Target: "/nix"}},
			Env: []string{"PACT_STORAGE_ID=" + name, "PACT_STORAGE_PROVISIONED=" + strconv.FormatBool(config.Provisioned),
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
	for key, value := range config.Labels {
		if info.Labels[key] != value {
			return fmt.Errorf("refusing unowned Docker container %s", name)
		}
	}
	if info.Config.Image != config.Image {
		return fmt.Errorf("incompatible Nix daemon %s: running image %s, this Pact requires %s; use the matching Pact version or stop all sessions and upgrade the daemon before retrying", name, info.Config.Image, config.Image)
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
