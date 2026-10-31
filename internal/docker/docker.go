// Package docker implements Pact's Docker operations using the Docker CLI.
// Callers retain resource ownership, persistence, and recovery policy.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

var (
	ErrNotFound = errors.New("object not found")
)

type Streams struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
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

// Inspect returns ErrNotFound if the object is missing
func Inspect(ctx context.Context, kind, name string) (Info, error) {
	data, err := output(ctx, kind, "inspect", "--format", "{{json .}}", name)
	if err != nil {
		exists, existsErr := Exists(ctx, kind, name)
		if existsErr != nil {
			return Info{}, errors.Join(err, existsErr)
		}

		if !exists {
			return Info{}, fmt.Errorf("%s %s: %w", kind, name, ErrNotFound)
		}

		return Info{}, err
	}

	var info Info
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		return Info{}, err
	}
	if kind == "container" {
		info.Labels = info.Config.Labels
	}
	return info, nil
}

func Exists(ctx context.Context, kind, name string) (bool, error) {
	var args []string

	safeName := "^" + regexp.QuoteMeta(name) + "$"
	switch kind {
	case "container":
		args = []string{"container", "ls", "--all", "--filter", "name=" + safeName, "--format", "{{.Names}}"}
	default:
		args = []string{kind, "ls", "--filter", "name=" + safeName, "--format", "{{.Name}}"}
	}

	out, err := output(ctx, args...)
	if err != nil {
		return false, err
	}

	return out != "", nil
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
