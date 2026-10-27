package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/docker"
)

// dindSocket is the daemon endpoint inside the agent container.
const dindSocket = "unix:///run/pact-docker/docker.sock"

// dindSocketDir is the mount point for the private socket volume.
const dindSocketDir = "/run/pact-docker"
const dindImage = "docker:29.8.1-dind@sha256:3f3c01aaaebf7cce837356b688b7c059a4749f10bd7660dec7c58fc454a283f0"

// startDIND creates a private sidecar and returns its socket volume.
// Call the returned cleanup function when finished. Startup errors clean up locally.
// It shares only the workspace; host credentials stay outside.
func startDIND(ctx context.Context, workspace string) (volume string, cleanup func() error, runErr error) {
	volume = "pact-dind-" + strings.ToLower(rand.Text())
	if err := docker.CreateVolume(ctx, volume, nil); err != nil {
		return "", nil, err
	}
	cleanup = func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := docker.RemoveContainer(cleanupCtx, volume, true, true)
		return errors.Join(err, docker.RemoveVolume(cleanupCtx, volume))
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, cleanup())
		}
	}()
	_, err := docker.CreateContainer(ctx, docker.ContainerConfig{
		Name: volume, Image: dindImage, Privileged: true, Env: []string{"DOCKER_TLS_CERTDIR="},
		Mounts: []docker.Mount{
			{Type: "volume", Source: volume, Target: dindSocketDir},
			{Type: "bind", Source: workspace, Target: "/home/pact/workspace"},
		},
		Command: []string{"dockerd", "--host=" + dindSocket},
	})
	if err == nil {
		err = docker.StartContainer(ctx, volume)
	}
	if err != nil {
		return volume, cleanup, fmt.Errorf("start DinD sidecar: %w", errors.Join(err, context.Cause(ctx)))
	}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for readyCtx.Err() == nil {
		if docker.Exec(readyCtx, volume, []string{"docker", "--host", dindSocket, "info"}, docker.Streams{}) == nil {
			// The socket is in a private per-execution volume. Permit the remapped
			// agent UID to use it without relying on a host docker group ID.
			if err := docker.Exec(readyCtx, volume, []string{"chmod", "0666", "/run/pact-docker/docker.sock"}, docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
				return volume, cleanup, errors.Join(err, context.Cause(readyCtx))
			}
			return volume, cleanup, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-readyCtx.Done():
			timer.Stop()
		}
	}
	if ctx.Err() != nil {
		return volume, cleanup, fmt.Errorf("start DinD sidecar: %w", context.Cause(ctx))
	}
	logsCtx, cancelLogs := context.WithTimeout(ctx, 5*time.Second)
	_ = docker.Logs(logsCtx, volume, 0, docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr})
	cancelLogs()
	return volume, cleanup, fmt.Errorf("DinD sidecar did not become ready within 60 seconds: %w", context.Cause(readyCtx))
}
