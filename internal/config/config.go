// Package config loads the user's Pact settings without external dependencies.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Git       Git                  `json:"git,omitzero"`
	MCP       map[string]MCPServer `json:"mcp,omitempty"`
	Approvals Approvals            `json:"approvals,omitzero"`
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

// Path returns an absolute config path, using the host's default when empty.
func Path(path string) (string, error) {
	if path == "" {
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			dir = filepath.Join(home, ".config")
		}
		path = filepath.Join(dir, "pact", "config.json")
	}
	return filepath.Abs(path)
}

// Load reads path, or $XDG_CONFIG_HOME/pact/config.json (defaulting to
// ~/.config/pact/config.json) when path is empty. A missing default config is
// allowed; an explicit path must exist. Relative credential paths are resolved
// against the config's directory; ~/ uses the host user's home directory.
func Load(path string) (Config, error) {
	explicit := path != ""
	path, err := Path(path)
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("open Pact config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode Pact config %s: %w", path, err)
	}
	if cfg.Git.SSHKey != "" {
		cfg.Git.SSHKey, err = resolvePath(path, cfg.Git.SSHKey)
		if err != nil {
			return Config{}, fmt.Errorf("resolve git.sshKey: %w", err)
		}
	}
	if cfg.Git.KnownHosts != "" {
		cfg.Git.KnownHosts, err = resolvePath(path, cfg.Git.KnownHosts)
		if err != nil {
			return Config{}, fmt.Errorf("resolve git.knownHosts: %w", err)
		}
	}
	if cfg.Approvals.Socket != "" {
		cfg.Approvals.Socket, err = resolvePath(path, cfg.Approvals.Socket)
		if err != nil {
			return Config{}, fmt.Errorf("resolve approvals.socket: %w", err)
		}
	}
	for name, server := range cfg.MCP {
		if server.TokenFile == "" {
			continue
		}
		server.TokenFile, err = resolvePath(path, server.TokenFile)
		if err != nil {
			return Config{}, fmt.Errorf("resolve mcp.%s.tokenFile: %w", name, err)
		}
		cfg.MCP[name] = server
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
