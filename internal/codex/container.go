package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/harness"
)

const stateDir = "/home/pact/.codex"

// PrepareContainer projects Codex login/state and supplies the native runner.
// The caller owns the returned cleanup for the host authentication snapshot.
func PrepareContainer(config *docker.ContainerConfig, stateVolume string, options harness.Options) (func() error, error) {
	request, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("encode launch options: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	authFile, err := CreateAuthSnapshot(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return nil, err
	}
	config.Mounts = append(config.Mounts,
		docker.Mount{Type: "volume", Source: stateVolume, Target: stateDir},
		docker.Mount{Type: "bind", Source: authFile, Target: stateDir + "/auth.json", ReadOnly: true},
	)
	config.Env = append(config.Env, "CODEX_HOME="+stateDir,
		"PACT_AGENT_STATE_DIR="+stateDir, "PACT_AGENT_AUTH_FILE="+stateDir+"/auth.json")
	config.TTY, config.Interactive = options.Prompt == "", options.Prompt == ""
	config.Command = []string{"pact-agent", "codex", string(request)}
	return func() error { return os.Remove(authFile) }, nil
}
