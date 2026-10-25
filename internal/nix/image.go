package container

import (
	"context"
	"io/fs"

	"github.com/alexghr/pact/internal/docker"
)

// BuildAgentImage builds the embedded agent image if its context hash is not cached.
func BuildAgentImage(ctx context.Context) (string, error) {
	return docker.BuildImage(ctx, BuildContext, "pact-codex")
}

// BuildDaemonImage hashes only upstream Nix and its service configuration.
// Toolset changes do not change daemon compatibility.
func BuildDaemonImage(ctx context.Context) (string, error) {
	files, err := fs.Sub(BuildContext, "daemon")
	if err != nil {
		return "", err
	}
	return docker.BuildImage(ctx, files, "pact-nix-daemon")
}
