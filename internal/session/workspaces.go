package session

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

	"github.com/alexghr/pact/internal/state"
)

func workspaceIdentity(info os.FileInfo) string {
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
}

func checkWorkspaceOverlap(ctx context.Context, store *state.Store, w state.Workspace) error {
	workspaces, err := store.Workspaces(ctx)
	if err != nil {
		return err
	}
	for _, other := range workspaces {
		if other.ID == w.ID {
			continue
		}
		for _, pair := range [][2]string{{w.Path, other.Path}, {other.Path, w.Path}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || rel == "." || filepath.IsLocal(rel) {
				return fmt.Errorf("workspace %s overlaps another registration at %s; resolve its references before removal", w.Path, other.Path)
			}
		}
	}
	return nil
}

// RemoveWorkspace only removes the recorded managed directory.
// cleanupPending is durable before deletion, so interruption never permits a
// launch into a partially removed directory. Retrying finishes the same intent.
func RemoveWorkspace(ctx context.Context, store *state.Store, id int64) error {
	w, err := store.Workspace(ctx, id)
	if err != nil {
		return err
	}
	if err := checkWorkspaceOverlap(ctx, store, w); err != nil {
		return err
	}
	if !w.Managed || w.Owner != fmt.Sprintf("uid:%d", os.Getuid()) {
		return errors.New("only this operator's catalogue-owned managed workspaces can be removed")
	}
	if w.Identity == "" || (w.Status != "ready" && w.Status != "cleanupPending" && w.Status != "removed") {
		return fmt.Errorf("workspace %s is %s without a completed allocation; recover it manually", w.Path, w.Status)
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(store.Path()), "workspaces"))
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
		w.Status = "removed"
		return store.UpdateWorkspace(ctx, w)
	}
	if err != nil {
		return fmt.Errorf("workspace needs recovery: %w", err)
	}
	if !info.IsDir() || workspaceIdentity(info) != w.Identity || w.Status == "removed" {
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
	if workspaceIdentity(info) != w.Identity {
		return errors.New("managed workspace changed while opening it; refusing removal")
	}
	w.Status = "cleanupPending"
	if err := store.UpdateWorkspace(ctx, w); err != nil {
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
	if !info.IsDir() || workspaceIdentity(info) != w.Identity {
		return errors.New("workspace path changed during cleanup; inspect it before retrying")
	}
	if err := parent.Remove(name); err != nil {
		return err
	}
	w.Status = "removed"
	if err := store.UpdateWorkspace(ctx, w); err != nil {
		return fmt.Errorf("workspace files removed; retry to record completion: %w", err)
	}
	return nil
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
	if w.Status == "removed" {
		return fmt.Errorf("workspace %s was intentionally removed; retained conversation history is still available", w.Path)
	}
	if w.Status == "cleanupPending" {
		return fmt.Errorf("workspace cleanup at %s is incomplete; retry its explicit removal before resuming", w.Path)
	}
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
