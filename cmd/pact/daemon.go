package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/alexghr/pact/internal/daemon"
	"github.com/alexghr/pact/internal/session"
)

type startConfig struct {
	newSessionConfig
	configPath string
	detach     bool
}

func serveDaemon(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("pact daemon", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: pact daemon")
	}
	return daemon.Serve(ctx)
}

func startDaemonSession(ctx context.Context, args []string) error {
	cfg, err := parseStartArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	return daemon.Start(ctx, session.Options{
		Workspace: cfg.workspace, Title: cfg.title, Model: cfg.model, Effort: cfg.effort,
		ConfigPath: cfg.configPath, Capabilities: session.Capabilities{DIND: cfg.dind, HostNetwork: cfg.hostNet},
	}, cfg.detach)
}

func parseStartArgs(args []string, output io.Writer) (startConfig, error) {
	var cfg startConfig
	flags := flag.NewFlagSet("pact start", flag.ContinueOnError)
	flags.SetOutput(output)
	addNewSessionFlags(flags, &cfg.newSessionConfig)
	flags.StringVar(&cfg.configPath, "config", "", "Config file (default: $XDG_CONFIG_HOME/pact/config.json or ~/.config/pact/config.json)")
	flags.BoolVar(&cfg.detach, "detach", false, "Return without attaching")
	flags.BoolVar(&cfg.detach, "d", false, "Alias for --detach")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact start [--detach] [--workspace PATH] [--model MODEL] [--effort EFFORT] [--config PATH]")
		fmt.Fprintln(output, "Enter prompts in the native TUI; detach with Ctrl-].")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return cfg, err
	}
	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	cfg.title = sessionTitle("")
	var err error
	cfg.workspace, err = resolveWorkspace(cfg.workspace)
	if err != nil {
		return cfg, err
	}
	if cfg.configPath != "" {
		cfg.configPath, err = filepath.Abs(cfg.configPath)
	}
	return cfg, err
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
