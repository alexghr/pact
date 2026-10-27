package codex

import (
	"fmt"
	"github.com/alexghr/pact/internal/docker"
)

const StateDir = "/home/pact/.codex"

// Options are Pact's requested run settings, before translation to native flags.
type Options struct {
	Prompt, Model, Effort, BackendRef string
	MCP                               map[string]string
}

func ConfigureContainer(config *docker.ContainerConfig, options Options) {
	config.TTY, config.Interactive = options.Prompt == "", options.Prompt == ""
	config.Command = []string{options.Prompt, options.Model, options.Effort}
	config.Env = append(config.Env, "PACT_RESUME_THREAD="+options.BackendRef)
	for name, endpoint := range options.MCP {
		config.Command = append(config.Command, "--config", fmt.Sprintf(`mcp_servers.%s={url=%q}`, name, endpoint))
	}
}
