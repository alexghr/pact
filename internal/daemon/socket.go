package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/alexghr/pact/internal/config"
)

// Both peers trust the same private directory. In particular, MkdirAll alone
// would accept a directory or symlink precreated by another user under /tmp.
func socketPath(create bool) (string, error) {
	dir := config.RuntimeDir()
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ok || stat.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("daemon socket directory %s must be owned by uid %d with mode 0700 and must not be a symlink", dir, os.Getuid())
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.sock"), nil
}
