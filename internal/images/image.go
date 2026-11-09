// Package images provides embedded Docker build contexts and image helpers.
package images

import (
	"context"
	"embed"
	"io/fs"

	"github.com/alexghr/pact/internal/docker"
)

//go:embed context
var contextFiles embed.FS

var (
	// Context necessary to build the Docker image the agent runs in. Frequently changes
	agentBuildContext, _ = fs.Sub(contextFiles, "context/agent")
	// The build context for the centralised Nix store. Changes very infrequently
	nixBuildContext, _ = fs.Sub(contextFiles, "context/nix")
)

const (
	agentImage = "pact-pi"
	nixImage   = "pact-nix"
)

func BuildAgentImage(ctx context.Context) (string, error) {
	return docker.BuildImage(ctx, agentBuildContext, agentImage)
}

func BuildNixImage(ctx context.Context) (string, error) {
	return docker.BuildImage(ctx, nixBuildContext, nixImage)
}

func GetNixImageName(ctx context.Context) (string, error) {
	return docker.GetImageName(ctx, nixBuildContext, nixImage)
}

// GetAgentFile reads a file from the agent image's build context for tests.
func GetAgentFile(path string) (string, error) {
	script, err := fs.ReadFile(agentBuildContext, path)
	if err != nil {
		return "", err
	}

	return string(script), nil
}
