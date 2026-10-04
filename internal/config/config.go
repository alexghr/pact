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
	Git *Git `json:"git,omitempty"`
}

type Git struct {
	Name   string `json:"name"`
	Email  string `json:"email"`
	SSHKey string `json:"ssh_key"`
}

// Load reads path, or the XDG user config directory's pact/config.json when
// path is empty. A missing default config is allowed; an explicit path must exist.
// Relative key paths are resolved against the config's directory; ~/ uses the
// host user's home directory.
func Load(path string) (Config, error) {
	explicit := path != ""
	if !explicit {
		dir, err := os.UserConfigDir()
		if err != nil {
			return Config{}, err
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
	if cfg.Git == nil || cfg.Git.SSHKey == "" {
		return cfg, nil
	}
	key := cfg.Git.SSHKey
	if strings.HasPrefix(key, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		key = filepath.Join(home, key[2:])
	} else if !filepath.IsAbs(key) {
		key = filepath.Join(filepath.Dir(path), key)
	}
	key, err = filepath.Abs(key)
	if err != nil {
		return Config{}, fmt.Errorf("resolve git.ssh_key: %w", err)
	}
	cfg.Git.SSHKey = key
	return cfg, nil
}
