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
	Git           Git                  `json:"git,omitzero"`
	MCP           map[string]MCPServer `json:"mcp,omitempty"`
	Approvals     Approvals            `json:"approvals,omitzero"`
	DatabasePath  string               `json:"databasePath"`
	WorkspaceRoot string               `json:"workspaceRoot"`
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

	if cfg.DatabasePath == "" || cfg.WorkspaceRoot == "" {
		dir, err := defaultStateDir()
		if err != nil {
			return HostConfig{}, err
		}
		if cfg.DatabasePath == "" {
			cfg.DatabasePath = filepath.Join(dir, "store.sqlite")
		}
		if cfg.WorkspaceRoot == "" {
			cfg.WorkspaceRoot = filepath.Join(dir, "workspaces")
		}
	}
	for _, setting := range []struct {
		name  string
		value *string
	}{
		{"databasePath", &cfg.DatabasePath},
		{"workspaceRoot", &cfg.WorkspaceRoot},
	} {
		*setting.value, err = resolvePath(path, *setting.value)
		if err != nil {
			return HostConfig{}, fmt.Errorf("resolve %s: %w", setting.name, err)
		}
	}
	if err := cfg.validatePaths(path); err != nil {
		return HostConfig{}, err
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

func defaultStateDir() (string, error) {
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

	return filepath.Join(dir, "pact"), nil
}

// Only the configured managed root is constrained. Operators choose what an
// explicit -w workspace exposes, including any host files inside it.
func (cfg HostConfig) validatePaths(configPath string) error {
	root, err := canonicalPath(cfg.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve workspaceRoot: %w", err)
	}
	for _, target := range []struct{ name, path string }{
		{"databasePath", cfg.DatabasePath},
		{"config directory", filepath.Dir(configPath)},
		{"config file", configPath},
	} {
		path, err := canonicalPath(target.path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", target.name, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || filepath.IsLocal(rel) {
			return fmt.Errorf("workspaceRoot %s must not contain %s (%s)", root, target.name, path)
		}
	}
	return nil
}

// Resolve existing ancestors so a first launch can validate paths before the
// database or workspace root exists. A dangling symlink still returns an error.
func canonicalPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); err == nil {
		return filepath.EvalSymlinks(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, err := canonicalPath(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
