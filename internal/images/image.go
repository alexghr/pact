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
	nixBuildContext, _   = fs.Sub(contextFiles, "context/nix")
)

const (
	agentImage = "pact-codex"
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
