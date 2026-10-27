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
	_, err = s.db.ExecContext(ctx, "UPDATE workspaces SET identity = ?, status = 'ready' WHERE id = ?", identity, launch.Storage.WorkspaceID)
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
	return fileIdentity(info), nil
}

func fileIdentity(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func (s *Store) checkWorkspace(w Workspace) error {
	if w.Status == "removed" {
		return fmt.Errorf("workspace %s was intentionally removed; retained conversation history is still available", w.Path)
	}
	if w.Status == "cleanupPending" {
		return fmt.Errorf("workspace cleanup at %s is incomplete; retry its explicit removal before resuming", w.Path)
	}
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

// Workspaces returns recorded directories without inspecting the filesystem.
func (s *Store) Workspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, path, owner, managed, status, identity FROM workspaces ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Path, &w.Owner, &w.Managed, &w.Status, &w.Identity); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (s *Store) UpdateWorkspace(ctx context.Context, w Workspace) error {
	result, err := s.db.ExecContext(ctx, "UPDATE workspaces SET status = ?, identity = ? WHERE id = ?", w.Status, w.Identity, w.ID)
	return executionUpdate(result, err)
}
