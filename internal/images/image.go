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
	// TODO: this should be private. We should expose a "BuildTools" function here.
	ToolsBuildContext, _ = fs.Sub(contextFiles, "context/tools")
	// Context necessary to build the Docker image the agent runs in. Frequently changes
	agentBuildContext, _ = fs.Sub(contextFiles, "context/agent")
	// The build context for the centralised Nix store. Changes very infrequently
	nixBuildContext, _ = fs.Sub(contextFiles, "context/nix")
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

// Get files from the agent image's build context. Used for testing
func GetAgentFile(path string) (string, error) {
	script, err := fs.ReadFile(agentBuildContext, path)
	if err != nil {
		return "", err
	}

	return string(script), nil
}
