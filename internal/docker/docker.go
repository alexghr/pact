// Package docker implements Pact's Docker operations using the Docker CLI.
// Callers retain resource ownership, persistence, and recovery policy.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Streams struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

func labelsArgs(labels map[string]string) []string {
	var args []string
	for key, value := range labels {
		args = append(args, "--label", key+"="+value)
	}
	return args
}

func output(ctx context.Context, args ...string) (string, error) {
	data, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func run(ctx context.Context, streams Streams, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	return cmd.Run()
}

// Info is an observation from Docker, without Pact-specific ownership policy.
type Info struct {
	ID, Image string
	Labels    map[string]string
	Config    struct {
		Image  string
		Labels map[string]string
	}
	State struct{ Running, Paused, Restarting bool }
}

// Inspect distinguishes absence from daemon failure before inspecting identity.
func Inspect(ctx context.Context, kind, name string) (*Info, error) {
	args := []string{kind, "ls", "--filter", "name=" + name, "--format", "{{.Name}}"}
	if kind == "container" {
		args = []string{"container", "ls", "--all", "--filter", "name=" + name, "--format", "{{.Names}}"}
	}
	names, err := output(ctx, args...)
	if err != nil {
		return nil, err
	}
	if !strings.Contains("\n"+names+"\n", "\n"+name+"\n") {
		return nil, nil
	}
	data, err := output(ctx, kind, "inspect", "--format", "{{json .}}", name)
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return nil, err
	}
	if kind == "container" {
		info.Labels = info.Config.Labels
	}
	return &info, nil
}
