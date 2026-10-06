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
	Git *Git                 `json:"git,omitempty"`
	MCP map[string]MCPServer `json:"mcp,omitempty"`
}

type MCPServer struct {
	URL       string `json:"url"`
	Token     string `json:"token,omitempty"`
	TokenFile string `json:"tokenFile,omitempty"`
}

type Git struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	SSHKey string `json:"ssh_key"`
}

// Load reads path, or $XDG_CONFIG_HOME/pact/config.json (defaulting to
// ~/.config/pact/config.json) when
// path is empty. A missing default config is allowed; an explicit path must exist.
// Relative credential paths are resolved against the config's directory; ~/ uses the
// host user's home directory.
func Load(path string) (Config, error) {
	explicit := path != ""
	if !explicit {
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return Config{}, err
			}
			dir = filepath.Join(home, ".config")
		}
		path = filepath.Join(dir, "pact", "config.json")
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
	if cfg.Git != nil && cfg.Git.SSHKey != "" {
		cfg.Git.SSHKey, err = resolvePath(path, cfg.Git.SSHKey)
		if err != nil {
			return Config{}, fmt.Errorf("resolve git.ssh_key: %w", err)
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
