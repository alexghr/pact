package dind

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

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
	sidecar, err := owner.CreateContainer(ctx, "pact-dind-", dindImage, []string{"--privileged",
		"--env", "DOCKER_TLS_CERTDIR=",
		"--volume", volume.Name + ":/run/pact-docker",
		"--volume", workspace + ":/home/pact/workspace",
		dindImage, "dockerd", "--host=" + Socket})
	if err == nil {
		err = runDocker(ctx, "start", sidecar.ContainerID)
	}
	if err != nil {
		return "", fmt.Errorf("start DinD sidecar: %w", errors.Join(err, context.Cause(ctx)))
	}
	name := sidecar.ContainerID
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for readyCtx.Err() == nil {
		cmd := exec.CommandContext(readyCtx, "docker", "exec", name, "docker", "--host", Socket, "info")
		if cmd.Run() == nil {
			// The socket is in a private per-session volume. Permit the remapped
			// agent UID to use it without relying on a host docker group ID.
			if err := runDocker(readyCtx, "exec", name, "chmod", "0666", "/run/pact-docker/docker.sock"); err != nil {
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
	_ = runDocker(logsCtx, "logs", name)
	cancelLogs()
	return "", fmt.Errorf("DinD sidecar did not become ready within 60 seconds: %w", context.Cause(readyCtx))
}

func runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
