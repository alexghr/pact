package dind

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Socket is the daemon endpoint inside the agent container.
const Socket = "unix:///run/pact-docker/docker.sock"

// SocketDir is the mount point for the private socket volume.
const SocketDir = "/run/pact-docker"
const dindImage = "docker:29.8.1-dind@sha256:3f3c01aaaebf7cce837356b688b7c059a4749f10bd7660dec7c58fc454a283f0"

// Start launches a sidecar and returns its socket volume name and cleanup function.
// It shares the workspace and agent authentication with the sidecar, but never
// passes the host Docker socket to the agent.
// The context controls startup; cleanup has an independent 10-second deadline.
func Start(ctx context.Context, workspace string) (string, func(), error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("pact-dind-%x", id)
	cleanup := func() {
		// Cancellation stops startup, but cleanup still needs time to remove the
		// resources it may have created. Bound both removals with a shared deadline.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runDocker(cleanupCtx, "rm", "--force", "--volumes", name); err != nil {
			fmt.Fprintln(os.Stderr, "Remove DinD sidecar:", err)
		}
		if err := runDocker(cleanupCtx, "volume", "rm", name); err != nil {
			fmt.Fprintln(os.Stderr, "Remove DinD socket volume:", err)
		}
	}
	// The volumes of the containers started in thesidecar are lost when the sidecar is closed.
	// Mount a volume at /var/lib/docker to retain them but then make sure only one container
	// uses the sidecar at a time
	err := runDocker(ctx, "run", "--detach", "--name", name, "--privileged",
		"--env", "DOCKER_TLS_CERTDIR=",
		"--volume", name+":/run/pact-docker",
		"--volume", workspace+":/home/pact/workspace",
		"--volume", "pact-codex-state:/home/pact/.codex:ro",
		dindImage, "dockerd", "--host="+Socket)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("start DinD sidecar: %w", errors.Join(err, context.Cause(ctx)))
	}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for readyCtx.Err() == nil {
		cmd := exec.CommandContext(readyCtx, "docker", "exec", name, "docker", "--host", Socket, "info")
		if cmd.Run() == nil {
			// The socket is in a private per-session volume. Permit the remapped
			// agent UID to use it without relying on a host docker group ID.
			if err := runDocker(readyCtx, "exec", name, "chmod", "0666", "/run/pact-docker/docker.sock"); err != nil {
				cleanup()
				return "", nil, errors.Join(err, context.Cause(readyCtx))
			}
			return name, cleanup, nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-readyCtx.Done():
			timer.Stop()
		}
	}
	if ctx.Err() != nil {
		cleanup()
		return "", nil, fmt.Errorf("start DinD sidecar: %w", context.Cause(ctx))
	}
	logsCtx, cancelLogs := context.WithTimeout(ctx, 5*time.Second)
	_ = runDocker(logsCtx, "logs", name)
	cancelLogs()
	cleanup()
	return "", nil, fmt.Errorf("DinD sidecar did not become ready within 60 seconds: %w", context.Cause(readyCtx))
}

func runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
