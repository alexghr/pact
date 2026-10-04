package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/dind"
	"github.com/alexghr/pact/internal/nix"
)

// agentConfig holds the options passed to the container's agent entrypoint.
// Pact's environment instructions are supplied by the container entrypoint.
type agentConfig struct {
	dind      bool
	workspace string
	prompt    string
	model     string
	effort    string
	config    string
	git       *config.Git
}

func parseArgs(args []string, output io.Writer) (agentConfig, error) {
	cfg := agentConfig{model: "gpt-6.1-sol"}
	flags := flag.NewFlagSet("pact", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.dind, "dind", false, "Give the agent a dedicated Docker-in-Docker daemon")
	flags.StringVar(&cfg.workspace, "workspace", "", "Workspace directory to mount (required)")
	flags.StringVar(&cfg.workspace, "w", "", "Alias for --workspace")
	flags.StringVar(&cfg.prompt, "prompt", "", "Prompt to run (omit for an interactive session)")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
	flags.StringVar(&cfg.model, "model", cfg.model, "Model to use")
	flags.StringVar(&cfg.model, "m", cfg.model, "Alias for --model")
	flags.StringVar(&cfg.effort, "effort", cfg.effort, "Reasoning effort (omit to use the model default)")
	flags.StringVar(&cfg.effort, "e", cfg.effort, "Alias for --effort")
	flags.StringVar(&cfg.config, "config", "", "Config file (default: $XDG_CONFIG_HOME/pact/config.json or ~/.config/pact/config.json)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact --workspace PATH [--prompt TEXT] [--model MODEL] [--effort EFFORT] [--config PATH]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	if cfg.workspace == "" {
		return cfg, errors.New("--workspace is required")
	}
	workspace, err := filepath.Abs(cfg.workspace)
	if err != nil {
		return cfg, err
	}
	cfg.workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return cfg, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(cfg.workspace)
	if err != nil {
		return cfg, err
	}
	if !info.IsDir() {
		return cfg, errors.New("workspace must be a directory")
	}
	return cfg, nil
}

func buildImage() error {
	context, err := os.MkdirTemp("", "pact-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(context)
	if err := os.CopyFS(context, container.BuildContext); err != nil {
		return fmt.Errorf("extract Docker build context: %w", err)
	}
	return runDocker("build", "--tag", "pact-codex", "--file", filepath.Join(context, "Dockerfile"), context)
}

func dockerArgs(cfg agentConfig, home string, uid, gid int, runtimeOptions ...string) []string {
	args := []string{"run", "--rm"}
	if cfg.prompt == "" {
		args = append(args, "-it")
	}
	args = append(args,
		"--volume", cfg.workspace+":/home/pact/workspace",
		"--volume", "pact-codex-state:/home/pact/.codex",
		"--volume", "pact-codex-state-nix:/nix",
		"--volume", filepath.Join(home, ".codex", "auth.json")+":/opt/pact/host-auth.json:ro",
		"--env", "HOST_UID="+strconv.Itoa(uid),
		"--env", "HOST_GID="+strconv.Itoa(gid),
	)
	if cfg.git != nil {
		args = append(args,
			"--env", "PACT_GIT_NAME="+cfg.git.Name,
			"--env", "PACT_GIT_EMAIL="+cfg.git.Email,
		)
		if cfg.git.SSHKey != "" {
			// Docker's --mount uses CSV, so quote paths that contain commas.
			var mount strings.Builder
			writer := csv.NewWriter(&mount)
			_ = writer.Write([]string{"type=bind", "source=" + cfg.git.SSHKey, "target=/opt/pact/host-git-key", "readonly"})
			writer.Flush()
			args = append(args,
				"--mount", strings.TrimSuffix(mount.String(), "\n"),
				"--env", "PACT_GIT_SSH_KEY=/opt/pact/host-git-key",
			)
		}
	}
	args = append(args, runtimeOptions...)
	return append(args, "pact-codex", cfg.prompt, cfg.model, cfg.effort)
}

func runDocker(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func run(args []string) error {
	cfg, err := parseArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	settings, err := config.Load(cfg.config)
	if err != nil {
		return err
	}
	cfg.git = settings.Git
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if err := buildImage(); err != nil {
		return err
	}
	var runtimeOptions []string
	if cfg.dind {
		name, cleanup, err := dind.Start(cfg.workspace)
		if err != nil {
			return err
		}
		defer cleanup()
		runtimeOptions = []string{"--volume", name + ":" + dind.SocketDir, "--env", "DOCKER_HOST=" + dind.Socket}
	}
	return runDocker(dockerArgs(cfg, home, os.Getuid(), os.Getgid(), runtimeOptions...)...)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}
