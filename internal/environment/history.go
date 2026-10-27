package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
)

// Threads asks the bundled harness to list saved conversations in its own store.
// The previous writer must be stopped. Codex may repair its native metadata;
// only catalogue metadata leaves the network-isolated helper. The entrypoint
// removes legacy saved authentication before opening native storage.
func Threads(ctx context.Context, env state.Environment, image string) (threads []harness.Thread, runErr error) {
	if err := owned(ctx, "volume", env.StateVolume, env.ID); err != nil {
		return nil, fmt.Errorf("read session history: %w", err)
	}
	if err := owned(ctx, "volume", env.NixVolume, env.NixVolume); err != nil {
		return nil, fmt.Errorf("read session tools: %w", err)
	}
	config := docker.ContainerConfig{Image: image, Network: "none",
		Mounts:  []docker.Mount{{Type: "volume", Source: env.StateVolume, Target: "/home/pact/.codex", NoCopy: true}},
		Env:     []string{"HOST_UID=" + strconv.Itoa(os.Getuid()), "HOST_GID=" + strconv.Itoa(os.Getgid())},
		Command: []string{"--list-threads"},
	}
	ConfigureContainer(env, &config)
	id, err := docker.CreateContainer(ctx, config)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, docker.RemoveContainer(cleanupCtx, id, true, true))
	}()
	var output, diagnostics bytes.Buffer
	if _, err := docker.Attach(ctx, id, docker.AttachOptions{Streams: docker.Streams{Stdout: &output, Stderr: &diagnostics}}); err != nil {
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
