package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
