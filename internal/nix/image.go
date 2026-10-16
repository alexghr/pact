package container

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuildAgentImage builds the embedded agent image if its context hash is not cached.
func BuildAgentImage(ctx context.Context) (string, error) {
	return buildImage(ctx, BuildContext, "pact-codex")
}

// BuildDaemonImage hashes only upstream Nix and its service configuration.
// Toolset changes do not change daemon compatibility.
func BuildDaemonImage(ctx context.Context) (string, error) {
	files, err := fs.Sub(BuildContext, "daemon")
	if err != nil {
		return "", err
	}
	return buildImage(ctx, files, "pact-nix-daemon")
}

func buildImage(ctx context.Context, files fs.FS, repository string) (string, error) {
	// Hash paths and contents in WalkDir's lexical order. This also accounts for
	// uncommitted build inputs and the generated Linux Git helpers.
	hash := sha256.New()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil // Continue walking nested files.
		}
		file, err := files.Open(path)
		if err != nil {
			return err
		}
		fileHash := sha256.New()
		_, err = io.Copy(fileHash, file)
		if err := errors.Join(err, file.Close()); err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%x", path, fileHash.Sum(nil))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash Docker build context: %w", err)
	}
	image := fmt.Sprintf("%s:%x", repository, hash.Sum(nil))
	// Listing distinguishes a missing image from a daemon or client failure.
	cmd := exec.CommandContext(ctx, "docker", "image", "ls", "--quiet", "--filter", "reference="+image)
	cmd.Stderr = os.Stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("look up Docker image: %w", err)
	}
	if strings.TrimSpace(string(output)) != "" {
		return image, nil
	}

	buildContext, err := os.MkdirTemp("", "pact-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(buildContext)
	if err := os.CopyFS(buildContext, files); err != nil {
		return "", fmt.Errorf("extract Docker build context: %w", err)
	}
	// Keep latest available for manual runs and the Docker integration check.
	cmd = exec.CommandContext(ctx, "docker", "build", "--tag", image, "--tag", repository, "--file", filepath.Join(buildContext, "Dockerfile"), buildContext)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build Docker image: %w", err)
	}
	return image, nil
}
