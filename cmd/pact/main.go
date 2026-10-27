package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alexghr/pact/internal/session"
)

const maxSessionTitleLength = 120

type agentConfig struct {
	hostNet, dind                            bool
	workspace, prompt, model, effort, config string
	resumeID                                 int64
}

func parseArgs(args []string, output io.Writer) (agentConfig, error) {
	cfg := agentConfig{model: "gpt-6.1-sol"}
	flags := flag.NewFlagSet("pact", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.dind, "dind", false, "Give the agent a dedicated Docker-in-Docker daemon")
	flags.BoolVar(&cfg.hostNet, "hostnet", false, "Run the container on the host's network")
	flags.StringVar(&cfg.workspace, "workspace", "", "Use an existing workspace directory (default: a fresh managed workspace)")
	flags.StringVar(&cfg.workspace, "w", "", "Alias for --workspace")
	flags.StringVar(&cfg.prompt, "prompt", "", "Prompt to run (omit for an interactive session)")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
	flags.StringVar(&cfg.model, "model", cfg.model, "Model to use")
	flags.StringVar(&cfg.model, "m", cfg.model, "Alias for --model")
	flags.StringVar(&cfg.effort, "effort", cfg.effort, "Reasoning effort (omit to use the model default)")
	flags.StringVar(&cfg.effort, "e", cfg.effort, "Alias for --effort")
	flags.StringVar(&cfg.config, "config", "", "Config file (default: $XDG_CONFIG_HOME/pact/config.json or ~/.config/pact/config.json)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact [--workspace PATH] [--prompt TEXT] [--model MODEL] [--effort EFFORT] [--config PATH]")
		fmt.Fprintln(output, "       pact workspaces remove SESSION_ID [--yes]")
		fmt.Fprintln(output, "       pact sessions list | show SESSION_ID")
		fmt.Fprintln(output, "       pact resume SESSION_ID [--prompt TEXT] [--config PATH]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	if cfg.workspace == "" {
		return cfg, nil
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

func run(args []string) (runErr error) {
	if len(args) > 0 {
		switch args[0] {
		case "sessions":
			return manageSessions(args[1:], os.Stdout)
		case "workspaces":
			return manageWorkspaces(args[1:], os.Stdout)
		}
	}
	var cfg agentConfig
	var err error
	if len(args) > 0 && args[0] == "resume" {
		cfg, err = parseResumeArgs(args[1:])
	} else {
		cfg, err = parseArgs(args, os.Stderr)
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := session.OpenStore(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	nix, err := session.PrepareNix(ctx)
	if err != nil {
		return err
	}
	var current *session.Session
	if cfg.resumeID > 0 {
		current, err = session.Restore(ctx, store, nix, cfg.resumeID, cfg.config)
	} else {
		current, err = session.New(ctx, store, nix, session.Options{
			Workspace: cfg.workspace, Title: sessionTitle(cfg.prompt), Model: cfg.model, Effort: cfg.effort,
			ConfigPath: cfg.config, Capabilities: session.Capabilities{DIND: cfg.dind, HostNetwork: cfg.hostNet},
		})
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Pact session %d\n", current.Record.ID)
	defer func() { runErr = errors.Join(runErr, current.Cleanup()) }()
	return current.Start(ctx, cfg.prompt)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}

func sessionTitle(prompt string) string {
	if prompt == "" {
		return "Interactive session"
	}
	title := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(title) > maxSessionTitleLength {
		return string(title[:maxSessionTitleLength]) + "…"
	}
	return string(title)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCode(err))
	}
}
