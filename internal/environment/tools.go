package environment

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/nix"
)

// realiseTools always asks Nix for this Pact's pinned toolset. Nix reuses valid
// outputs and atomically switches the shared profile only after a successful build.
func realiseTools(ctx context.Context, daemon string) error {
	files, err := fs.Sub(container.BuildContext, "tools")
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "pact-tools-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.CopyFS(dir, files); err != nil {
		return fmt.Errorf("extract toolset flake: %w", err)
	}
	if err := docker.CopyTo(ctx, dir+"/.", daemon, "/opt/pact/toolset"); err != nil {
		return err
	}
	if err := docker.Exec(ctx, daemon, []string{"nix", "--store", "daemon", "build",
		"/opt/pact/toolset#pact-tools", "--no-update-lock-file", "--no-link", "--profile", "/nix/var/nix/profiles/pact-tools"},
		docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr}); err != nil {
		return fmt.Errorf("realise Pact tools: %w", err)
	}
	// Keep the published directory stable: Codex watches skill directories, and
	// switching an ancestor profile symlink alone does not invalidate its cache.
	_, err = docker.ExecOutput(ctx, daemon, "bash", "-euc", `
skills=/nix/var/nix/pact-skills
profile=/nix/var/nix/profiles/pact-tools/share/pact/skills
mkdir -p "$skills"
for source in "$profile"/*; do
  [[ -d "$source" ]] || continue
  ln -sfn "$(readlink -f "$source")" "$skills/${source##*/}"
done
for link in "$skills"/*; do
  [[ -L "$link" ]] || continue
  [[ -d "$profile/${link##*/}" ]] || rm -- "$link"
done`)
	return err
}
