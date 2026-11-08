// Package config loads the user's Pact settings without external dependencies.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type HostConfig struct {
	Git          Git                  `json:"git,omitzero"`
	MCP          map[string]MCPServer `json:"mcp,omitempty"`
	Approvals    Approvals            `json:"approvals,omitzero"`
	DatabasePath string               `json:"databasePath"`
}

type MCPServer struct {
	URL           string   `json:"url"`
	Token         string   `json:"token,omitempty"`
	TokenFile     string   `json:"tokenFile,omitempty"`
	AllowedTools  []string `json:"allowedTools,omitempty"`
	ApprovalTools []string `json:"approvalTools,omitempty"`
}

type Approvals struct {
	Socket         string `json:"socket"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

type Git struct {
	Name           string   `json:"name"`
	Email          string   `json:"email"`
	SSHKey         string   `json:"sshKey"`
	KnownHosts     string   `json:"knownHosts,omitempty"`
	AllowedRemotes []string `json:"allowedRemotes,omitempty"`
}

// RuntimeDir selects Pact's per-user directory under XDG_RUNTIME_DIR, falling
// back to the host temporary directory. Callers create and validate it as needed.
func RuntimeDir() string {
	root := os.Getenv("XDG_RUNTIME_DIR")
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Join(root, fmt.Sprintf("pact-%d", os.Getuid()))
}

func DefaultHostPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}

	return filepath.Join(dir, "pact", "config.json"), nil
}

func InitEmptyConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}

		return fmt.Errorf("init config at %s: %w", path, err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(HostConfig{}); err != nil {
		return fmt.Errorf("write empty config at %s: %w", path, err)
	}

	return nil
}

func Load(path string) (HostConfig, error) {
	if path == "" {
		return HostConfig{}, errors.New("empty config path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return HostConfig{}, fmt.Errorf("open Pact config: %w", err)
	}
	var cfg HostConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return HostConfig{}, fmt.Errorf("decode Pact config %s: %w", path, err)
	}
	if cfg.Git.SSHKey != "" {
		cfg.Git.SSHKey, err = resolvePath(path, cfg.Git.SSHKey)
		if err != nil {
			return HostConfig{}, fmt.Errorf("resolve git.sshKey: %w", err)
		}
	}
	if cfg.Git.KnownHosts != "" {
		cfg.Git.KnownHosts, err = resolvePath(path, cfg.Git.KnownHosts)
		if err != nil {
			return HostConfig{}, fmt.Errorf("resolve git.knownHosts: %w", err)
		}
	}
	if cfg.Approvals.Socket != "" {
		cfg.Approvals.Socket, err = resolvePath(path, cfg.Approvals.Socket)
		if err != nil {
			return HostConfig{}, fmt.Errorf("resolve approvals.socket: %w", err)
		}
	}
	for name, server := range cfg.MCP {
		if server.TokenFile == "" {
			continue
		}
		server.TokenFile, err = resolvePath(path, server.TokenFile)
		if err != nil {
			return HostConfig{}, fmt.Errorf("resolve mcp.%s.tokenFile: %w", name, err)
		}
		cfg.MCP[name] = server
	}

	if cfg.DatabasePath == "" {
		db, err := defaultDatabase()
		if err != nil {
			return cfg, fmt.Errorf("resolve default database path: %s", err)
		}

		cfg.DatabasePath = db
	}

	return cfg, nil
}

func resolvePath(path, key string) (string, error) {
	if strings.HasPrefix(key, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		key = filepath.Join(home, key[2:])
	} else if !filepath.IsAbs(key) {
		key = filepath.Join(filepath.Dir(path), key)
	}
	return filepath.Abs(key)
}

func defaultDatabase() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")

	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find catalogue home: %w", err)
		}
		dir = filepath.Join(home, ".local", "state")
	}

	if !filepath.IsAbs(dir) {
		return "", errors.New("catalogue state directory must be absolute")
	}

	return filepath.Join(dir, "pact", "sessions.db"), nil
}
