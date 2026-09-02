package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alexghr/pact/internal/docker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := "run"
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--resume-last" {
		mode = "resume-last"
		args = args[1:]
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: pact [--resume-last] WORKSPACE PROMPT [MODEL] [EFFORT] [VARIANT]")
	}

	workspace, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return err
	}

	prompt := args[1]
	model := argument(args, 2, "gpt-5.6-sol")
	effort := argument(args, 3, "low")
	variant := argument(args, 4, "generic")
	image := "pact-codex:" + variant

	ctx := context.Background()
	var engine docker.Engine = docker.NewCLI(os.Stdin, os.Stdout, os.Stderr)
	if err := engine.Build(ctx, docker.BuildOptions{
		ContextDir: "./container",
		Dockerfile: filepath.Join("container", "Dockerfile."+variant),
		Tag:        image,
	}); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return engine.Run(ctx, docker.RunOptions{
		Image: image,
		Args:  []string{mode, prompt, model, effort},
		Env: []string{
			"HOST_UID=" + strconv.Itoa(os.Getuid()),
			"HOST_GID=" + strconv.Itoa(os.Getgid()),
		},
		Volumes: []string{
			workspace + ":/home/pact/workspace",
			"pact-codex-state:/home/pact/.codex",
			filepath.Join(home, ".codex/auth.json") + ":/opt/pact/host-auth.json:ro",
		},
	})
}

func argument(args []string, index int, fallback string) string {
	if len(args) > index {
		return args[index]
	}
	return fallback
}
