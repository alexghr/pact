package dind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/execution"
)

// Socket is the daemon endpoint inside the agent container.
const Socket = "unix:///run/pact-docker/docker.sock"

// SocketDir is the mount point for the private socket volume.
const SocketDir = "/run/pact-docker"
const dindImage = "docker:29.8.1-dind@sha256:3f3c01aaaebf7cce837356b688b7c059a4749f10bd7660dec7c58fc454a283f0"

// Start creates a private sidecar whose intent and cleanup belong to the caller's
// execution owner. It shares only the workspace; host credentials stay outside.
func Start(ctx context.Context, owner *execution.Owner, workspace string) (string, error) {
	volume, err := owner.CreateVolume(ctx)
	if err != nil {
		return "", err
	}
	sidecar, err := owner.CreateContainer(ctx, "pact-dind-", docker.ContainerConfig{
		Image: dindImage, Privileged: true, Env: []string{"DOCKER_TLS_CERTDIR="},
		Mounts: []docker.Mount{
			{Type: "volume", Source: volume.Name, Target: SocketDir},
			{Type: "bind", Source: workspace, Target: "/home/pact/workspace"},
		},
		Command: []string{"dockerd", "--host=" + Socket},
	})
	if err == nil {
		err = docker.StartContainer(ctx, sidecar.ContainerID)
	}
	if err != nil {
		return "", fmt.Errorf("start DinD sidecar: %w", errors.Join(err, context.Cause(ctx)))
	}
	name := sidecar.ContainerID
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for readyCtx.Err() == nil {
		if docker.Exec(readyCtx, name, []string{"docker", "--host", Socket, "info"}, docker.Streams{}) == nil {
			// The socket is in a private per-session volume. Permit the remapped
			// agent UID to use it without relying on a host docker group ID.
			if err := docker.Exec(readyCtx, name, []string{"chmod", "0666", "/run/pact-docker/docker.sock"}, docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
				return "", errors.Join(err, context.Cause(readyCtx))
			}
			return volume.Name, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-readyCtx.Done():
			timer.Stop()
		}
	}
	if ctx.Err() != nil {
		return "", fmt.Errorf("start DinD sidecar: %w", context.Cause(ctx))
	}
	logsCtx, cancelLogs := context.WithTimeout(ctx, 5*time.Second)
	_ = docker.Logs(logsCtx, name, 0, docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr})
	cancelLogs()
	return "", fmt.Errorf("DinD sidecar did not become ready within 60 seconds: %w", context.Cause(readyCtx))
}
