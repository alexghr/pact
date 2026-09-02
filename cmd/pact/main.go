package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
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
		Dockerfile: filepath.Join("container", variant+".Dockerfile"),
		Tag:        image,
	}); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	store, err := state.Open(ctx, filepath.Join(home, ".local", "state", "pact", "pact.db"))
	if err != nil {
		return err
	}

	runID, err := store.StartRun(ctx, state.Run{
		WorkspaceDir:      workspace,
		Model:             model,
		Effort:            effort,
		DockerfileVariant: variant,
	})
	if err != nil {
		return errors.Join(err, store.Close())
	}

	runErr := engine.Run(ctx, docker.RunOptions{
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

	status := "finished"
	exitCode := 0
	var storedExitCode *int = &exitCode
	if runErr != nil {
		status = "error"
		storedExitCode = processExitCode(runErr)
	}
	completeErr := store.CompleteRun(ctx, runID, status, storedExitCode)
	closeErr := store.Close()
	return errors.Join(runErr, completeErr, closeErr)
}

func processExitCode(err error) *int {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return nil
	}
	code := exitError.ExitCode()
	return &code
}

func argument(args []string, index int, fallback string) string {
	if len(args) > index {
		return args[index]
	}
	return fallback
}
