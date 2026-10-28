package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alexghr/pact/internal/state"
)

func workspaceIdentity(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func newWorkspacePath(store *state.Store) (string, error) {
	root := filepath.Join(filepath.Dir(store.Path()), "workspaces")
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
	identity, err := directoryIdentity(w.Path)
	if err != nil {
		return err
	}
	w.Identity, w.Status = identity, "ready"
	return store.UpdateWorkspace(ctx, w)
}

func directoryIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace must be a directory, not a symlink")
	}
	return workspaceIdentity(info), nil
}

func checkWorkspace(store *state.Store, w state.Workspace) error {
	if w.Status != "ready" {
		return fmt.Errorf("workspace %s is %s; inspect the session and recover it explicitly", w.Path, w.Status)
	}
	path, err := resolveWorkspace(store, w.Path)
	if err != nil {
		return fmt.Errorf("saved workspace needs recovery: %w", err)
	}
	if path != w.Path {
		return errors.New("saved workspace now resolves to a different directory; restore its original location before resuming")
	}
	if w.Managed {
		identity, err := directoryIdentity(path)
		if err != nil {
			return err
		}
		if identity != w.Identity || identity == "" {
			return fmt.Errorf("managed workspace %s was replaced; restore the original directory before resuming", path)
		}
	}
	return nil
}

func resolveWorkspace(store *state.Store, workspace string) (string, error) {
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
	path, err := filepath.EvalSymlinks(store.Path())
	if err != nil {
		return "", fmt.Errorf("resolve catalogue: %w", err)
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil {
		return "", err
	}
	if rel == "." || filepath.IsLocal(rel) {
		return "", errors.New("catalogue must be outside the mounted workspace")
	}
	return workspace, nil
}
