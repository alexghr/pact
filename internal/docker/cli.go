package docker

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

type CLI struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

var _ Engine = (*CLI)(nil)

func NewCLI(stdin io.Reader, stdout, stderr io.Writer) *CLI {
	return &CLI{stdin: stdin, stdout: stdout, stderr: stderr}
}

func (c *CLI) Build(ctx context.Context, options BuildOptions) error {
	if err := c.run(ctx, buildArgs(options)); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

func (c *CLI) Run(ctx context.Context, options RunOptions) error {
	if err := c.run(ctx, runArgs(options)); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	return nil
}

func (c *CLI) run(ctx context.Context, args []string) error {
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = c.stdin
	command.Stdout = c.stdout
	command.Stderr = c.stderr
	return command.Run()
}

func buildArgs(options BuildOptions) []string {
	return []string{
		"build",
		"--tag", options.Tag,
		"--file", options.Dockerfile,
		options.ContextDir,
	}
}

func runArgs(options RunOptions) []string {
	args := []string{"run", "--rm"}
	for _, volume := range options.Volumes {
		args = append(args, "--volume", volume)
	}
	for _, environment := range options.Env {
		args = append(args, "--env", environment)
	}
	args = append(args, options.Image)
	return append(args, options.Args...)
}
