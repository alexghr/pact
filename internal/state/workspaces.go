package state

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
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

// checkWorkspaceOverlap refuses deletion through an overlapping registration.
func (s *Store) checkWorkspaceOverlap(ctx context.Context, w Workspace) error {
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM workspaces WHERE id <> ?", w.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		for _, pair := range [][2]string{{w.Path, path}, {path, w.Path}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || rel == "." || filepath.IsLocal(rel) {
				rows.Close()
				return fmt.Errorf("workspace %s overlaps another registration at %s; resolve its references before removal", w.Path, path)
			}
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// RemoveWorkspace only removes the recorded managed directory.
// cleanupPending is durable before deletion, so interruption never permits a
// launch into a partially removed directory. Retrying finishes the same intent.
func (s *Store) RemoveWorkspace(ctx context.Context, id int64) error {
	w, err := s.Workspace(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkWorkspaceOverlap(ctx, w); err != nil {
		return err
	}
	if !w.Managed || w.Owner != fmt.Sprintf("uid:%d", os.Getuid()) {
		return errors.New("only this operator's catalogue-owned managed workspaces can be removed")
	}
	if w.Identity == "" || (w.Status != "ready" && w.Status != "cleanupPending" && w.Status != "removed") {
		return fmt.Errorf("workspace %s is %s without a completed allocation; recover it manually", w.Path, w.Status)
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(s.path), "workspaces"))
	if err != nil {
		return err
	}
	if filepath.Dir(w.Path) != root {
		return errors.New("managed workspace storage was redirected; restore its original location")
	}
	parent, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(w.Path)
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && (w.Status == "cleanupPending" || w.Status == "removed") {
		_, err = s.db.ExecContext(ctx, "UPDATE workspaces SET status = 'removed' WHERE id = ?", w.ID)
		return err
	}
	if err != nil {
		return fmt.Errorf("workspace needs recovery: %w", err)
	}
	if !info.IsDir() || fileIdentity(info) != w.Identity || w.Status == "removed" {
		return errors.New("managed workspace was redirected or replaced; refusing removal")
	}
	if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New("managed workspace is no longer owned by this operator")
	}
	dir, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err = dir.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect open workspace: %w", err)
	}
	if fileIdentity(info) != w.Identity {
		return errors.New("managed workspace changed while opening it; refusing removal")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE workspaces SET status = 'cleanupPending' WHERE id = ?", w.ID); err != nil {
		return err
	}
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := dir.RemoveAll(entry.Name()); err != nil {
			return fmt.Errorf("workspace cleanup is incomplete; correct the error and retry removal: %w", err)
		}
	}
	info, err = parent.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect workspace path before completing cleanup: %w", err)
	}
	if !info.IsDir() || fileIdentity(info) != w.Identity {
		return errors.New("workspace path changed during cleanup; inspect it before retrying")
	}
	if err := parent.Remove(name); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE workspaces SET status = 'removed' WHERE id = ?", w.ID)
	if err != nil {
		return fmt.Errorf("workspace files removed; retry to record completion: %w", err)
	}
	return nil
}
