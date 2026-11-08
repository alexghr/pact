package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexghr/pact/internal/state"
)

func newWorkspacePath(root string) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create workspace storage: %w", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, strings.ToLower(rand.Text())), nil
}

// The catalogue records allocation intent before touching this directory.
func allocateWorkspace(ctx context.Context, store *state.Store, w state.Workspace) error {
	if err := os.Mkdir(w.Path, 0700); err != nil {
		return err
	}
	w.Status = "ready"
	return store.UpdateWorkspace(ctx, w)
}

func checkWorkspace(w state.Workspace) error {
	if w.Status != "ready" {
		return fmt.Errorf("workspace %s is %s; inspect the session and recover it explicitly", w.Path, w.Status)
	}
	path, err := resolveWorkspace(w.Path)
	if err != nil {
		return fmt.Errorf("saved workspace needs recovery: %w", err)
	}
	if path != w.Path {
		return errors.New("saved workspace now resolves to a different directory; restore its original location before resuming")
	}
	return nil
}

func resolveWorkspace(workspace string) (string, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace must be a directory")
	}
	return workspace, nil
}
