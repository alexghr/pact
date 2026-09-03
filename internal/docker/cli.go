package docker

import (
	"context"
	"errors"
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

func (c *CLI) Run(ctx context.Context, options RunOptions, handleIO IOHandler) error {
	command := exec.CommandContext(ctx, "docker", runArgs(options)...)
	// we need stdout and stdin to communicate with the Codex app-server
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("docker run stdout: %w", err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("docker run stdin: %w", err)
	}
	// stderr can just be forwarded to the terminal
	command.Stderr = c.stderr

	if err := command.Start(); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}
	handleErr := handleIO(ctx, stdout, stdin)
	closeErr := stdin.Close()
	waitErr := command.Wait()
	if err := errors.Join(handleErr, closeErr, waitErr); err != nil {
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
	args := []string{"run", "--rm", "--interactive"}
	for _, volume := range options.Volumes {
		args = append(args, "--volume", volume)
	}
	for _, environment := range options.Env {
		args = append(args, "--env", environment)
	}
	args = append(args, options.Image)
	return args
}
