package pi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/harness"
)

const stateDir = "/home/pact/.pi/agent"

// PrepareContainer projects host credentials and model definitions into a
// session's private Pi state. The caller owns the returned snapshot cleanup.
func PrepareContainer(config *docker.ContainerConfig, stateVolume string, options harness.Options) (func() error, error) {
	request, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("encode launch options: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	snapshots, err := createSnapshots(filepath.Join(home, ".pi", "agent"))
	if err != nil {
		return nil, err
	}
	config.Mounts = append(config.Mounts,
		docker.Mount{Type: "volume", Source: stateVolume, Target: stateDir},
		docker.Mount{Type: "bind", Source: filepath.Join(snapshots, "auth.json"), Target: stateDir + "/auth.json", ReadOnly: true},
		docker.Mount{Type: "bind", Source: filepath.Join(snapshots, "models.json"), Target: stateDir + "/models.json", ReadOnly: true},
	)
	config.Env = append(config.Env, "PI_CODING_AGENT_DIR="+stateDir,
		"PACT_AGENT_STATE_DIR="+stateDir, "PACT_AGENT_AUTH_FILE="+stateDir+"/auth.json",
		"PACT_AGENT_MODELS_FILE="+stateDir+"/models.json")
	config.TTY, config.Interactive = true, true
	config.Command = []string{"pact-agent", "pi", string(request)}
	return func() error { return os.RemoveAll(snapshots) }, nil
}

func createSnapshots(agentDir string) (string, error) {
	auth, err := authSnapshot(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		return "", err
	}
	models, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if errors.Is(err, fs.ErrNotExist) {
		models = []byte("{}")
	} else if err != nil {
		return "", fmt.Errorf("read host Pi models: %w", err)
	}

	dir, err := os.MkdirTemp("", "pact-pi-*")
	if err != nil {
		return "", fmt.Errorf("create Pi snapshots: %w", err)
	}
	for name, data := range map[string][]byte{"auth.json": auth, "models.json": models} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("write Pi snapshot: %w", err)
		}
	}
	return dir, nil
}
