package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRootLayout(t *testing.T) {
	base := t.TempDir()
	configDir := filepath.Join(base, "config")
	for _, dir := range []string{"config", "workspaces"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []struct{ path, target string }{
		{"config-link", "config"},
		{"database-link", "workspaces"},
		{"config/linked.json", "workspaces/config.json"},
		{"dangling", "missing"},
	} {
		if err := os.Symlink(filepath.Join(base, link.target), filepath.Join(base, link.path)); err != nil {
			t.Fatal(err)
		}
	}

	for _, tt := range []struct {
		name, workspaceRoot, databasePath, configFile, wantError string
	}{
		{"first launch", "../new/workspaces", "../state/store.sqlite", "config.json", ""},
		{"relative paths", "managed", "state/store.sqlite", "config.json", ""},
		{"sibling with shared prefix", "../workspaces", "../workspaces-state/store.sqlite", "config.json", ""},
		{"nested database", "../workspaces", "../workspaces/new/store.sqlite", "config.json", "must not contain databasePath"},
		{"database equals root", "../workspaces", "../workspaces", "config.json", "must not contain databasePath"},
		{"config directory", ".", "../state/store.sqlite", "config.json", "must not contain config directory"},
		{"root symlink", "../config-link", "../state/store.sqlite", "config.json", "must not contain config directory"},
		{"database parent symlink", "../workspaces", "../database-link/store.sqlite", "config.json", "must not contain databasePath"},
		{"config file symlink", "../workspaces", "../state/store.sqlite", "linked.json", "must not contain config file"},
		{"dangling root symlink", "../dangling", "../state/store.sqlite", "config.json", "resolve workspaceRoot"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := HostConfig{DatabasePath: tt.databasePath, WorkspaceRoot: tt.workspaceRoot}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(configDir, tt.configFile)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}

			loaded, err := Load(path)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("Load() error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if loaded.DatabasePath != filepath.Join(configDir, tt.databasePath) || loaded.WorkspaceRoot != filepath.Join(configDir, tt.workspaceRoot) {
				t.Fatalf("paths did not resolve against config directory: %+v", loaded)
			}
		})
	}
}
