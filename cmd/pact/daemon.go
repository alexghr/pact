package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/daemon"
	"github.com/alexghr/pact/internal/session"
)

type startConfig struct {
	newSessionConfig
	detach bool
	prompt string
}

func serveDaemon(ctx context.Context, args []string, hostCfg config.HostConfig) error {
	flags := flag.NewFlagSet("pact daemon", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: pact daemon")
	}
	return daemon.Serve(ctx, hostCfg)
}

func startDaemonSession(ctx context.Context, args []string, configPath string) error {
	cfg, err := parseStartArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	return startSession(ctx, cfg, configPath)
}

func startSession(ctx context.Context, cfg startConfig, configPath string) error {
	workspace, err := resolveWorkspace(cfg.workspace)
	if err != nil {
		return err
	}
	return daemon.Start(ctx, session.Options{
		Workspace: workspace, Title: "Interactive session", Model: cfg.model, Effort: cfg.effort, Prompt: cfg.prompt,
		ConfigPath: configPath, Capabilities: session.Capabilities{DIND: cfg.dind, HostNetwork: cfg.hostNet},
	}, cfg.detach)
}

func addStartFlags(flags *flag.FlagSet, cfg *startConfig) {
	addNewSessionFlags(flags, &cfg.newSessionConfig)
	addDetachFlags(flags, &cfg.detach)
	flags.StringVar(&cfg.prompt, "prompt", "", "Initial task input (not replayed on resume)")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
}

func addDetachFlags(flags *flag.FlagSet, detach *bool) {
	flags.BoolVar(detach, "detach", false, "Return without attaching")
	flags.BoolVar(detach, "d", false, "Alias for --detach")
}

func parseStartArgs(args []string, output io.Writer) (startConfig, error) {
	var cfg startConfig
	flags := flag.NewFlagSet("pact start", flag.ContinueOnError)
	flags.SetOutput(output)
	addStartFlags(flags, &cfg)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact [--config PATH] start [--detach] [--prompt TEXT] [--workspace PATH] [--model MODEL] [--effort EFFORT]")
		fmt.Fprintln(output, "Continue in the native TUI; detach with Ctrl-].")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	return cfg, nil
}

func attachSession(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pact attach SESSION_ID")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return errors.New("session ID must be a positive integer")
	}
	return daemon.Attach(ctx, id)
}
