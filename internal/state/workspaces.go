package state

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Workspace struct {
	ID       int64
	Path     string
	Owner    string
	Managed  bool
	Status   string
	Identity string
}

func (s *Store) Workspace(ctx context.Context, id int64) (Workspace, error) {
	var w Workspace
	err := s.db.QueryRowContext(ctx, "SELECT id, path, owner, managed, status, identity FROM workspaces WHERE id = ?", id).
		Scan(&w.ID, &w.Path, &w.Owner, &w.Managed, &w.Status, &w.Identity)
	return w, err
}

func (s *Store) newWorkspacePath() (string, error) {
	root := filepath.Join(filepath.Dir(s.path), "workspaces")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("create workspace storage: %w", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, strings.ToLower(rand.Text())), nil
}

// Provisioning intent and the session already exist before touching this path.
// On failure leave both the records and any directory for explicit recovery.
func (s *Store) allocateWorkspace(ctx context.Context, launch Launch) error {
	if err := os.Mkdir(launch.Workspace, 0700); err != nil {
		return err
	}
	identity, err := directoryIdentity(launch.Workspace)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE workspaces SET identity = ?, status = 'ready' WHERE id = ?", identity, launch.Environment.WorkspaceID)
	return err
}

func directoryIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace must be a directory, not a symlink")
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func (s *Store) checkWorkspace(w Workspace) error {
	if w.Status != "ready" {
		return fmt.Errorf("workspace %s is %s; inspect the session and recover it explicitly", w.Path, w.Status)
	}
	path, err := s.resolveWorkspace(w.Path)
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
