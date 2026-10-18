package environment

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

// Threads asks the bundled harness to list saved conversations in its own store.
// The previous writer must be stopped. Codex may repair its native metadata;
// only catalogue metadata leaves the network-isolated helper.
func Threads(ctx context.Context, env state.Environment, image string) ([]harness.Thread, error) {
	if err := owned(ctx, "volume", env.StateVolume, env.ID); err != nil {
		return nil, fmt.Errorf("read session history: %w", err)
	}
	if err := owned(ctx, "volume", env.NixVolume, env.NixVolume); err != nil {
		return nil, fmt.Errorf("read session tools: %w", err)
	}
	name := "pact-history-" + strings.ToLower(rand.Text())
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", name).Run()
	}()
	args := []string{"run", "--name", name, "--network", "none",
		"--mount", "type=volume,src=" + env.StateVolume + ",dst=/home/pact/.codex,volume-nocopy",
		"--env", "HOST_UID=" + strconv.Itoa(os.Getuid()), "--env", "HOST_GID=" + strconv.Itoa(os.Getgid()),
	}
	args = append(args, Mounts(env)...)
	args = append(args, image, "--list-threads")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = 5 * time.Second
	var output, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostics
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("query saved conversations: %w: %s", err, strings.TrimSpace(diagnostics.String()))
	}
	var event harness.Event
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		return nil, fmt.Errorf("read saved conversations: %w", err)
	}
	if event.Type != "threads" {
		return nil, errors.New("harness did not return a saved conversation list")
	}
	return event.Threads, nil
}
