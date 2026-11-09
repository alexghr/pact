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
	"strconv"
	"strings"
	"syscall"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/daemon"
	"github.com/alexghr/pact/internal/version"
)

type newSessionConfig struct {
	hostNet, dind bool
	workspace     string
	model, effort string
}

// Flags accepted without an explicit start command.
type runConfig struct {
	startConfig
	configPath string
}

type resumeConfig struct {
	resumeID int64
	detach   bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()

	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCode(err))
	}
}

func run(ctx context.Context, args []string) error {
	cfg, args, err := parseArgs(args, os.Stderr)
	if err != nil {
		return err
	}

	if err := config.InitEmptyConfig(cfg.configPath); err != nil {
		return err
	}

	hostCfg, err := config.Load(cfg.configPath)
	if err != nil {
		return fmt.Errorf("load host config: %w", err)
	}

	if len(args) > 0 {
		switch args[0] {
		case "version":
			_, err := fmt.Fprintf(os.Stdout, "pact %s\n", version.Version)
			return err
		case "approvals":
			return serveApprovals(ctx, args[1:], hostCfg)
		case "sessions":
			return manageSessions(ctx, args[1:], os.Stdout, os.Stderr, hostCfg)
		case "resume":
			return resumeSession(ctx, args[1:], cfg.configPath)
		case "daemon":
			return serveDaemon(ctx, args[1:], hostCfg)
		case "start":
			return startDaemonSession(ctx, args[1:], cfg.configPath)
		case "attach":
			return attachSession(ctx, args[1:])
		default:
			return fmt.Errorf("unexpected argument: %s", args[0])
		}
	}

	return startSession(ctx, cfg.startConfig, cfg.configPath)
}

func parseArgs(args []string, output io.Writer) (runConfig, []string, error) {
	var cfg runConfig
	var err error

	flags := flag.NewFlagSet("pact", flag.ContinueOnError)
	flags.SetOutput(output)

	addStartFlags(flags, &cfg.startConfig)
	flags.StringVar(&cfg.configPath, "config", "", "Config file (default: $XDG_CONFIG_HOME/pact/config.json or ~/.config/pact/config.json)")

	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact [--config PATH] [--detach] [--workspace PATH] [--model MODEL] [--effort EFFORT]")
		fmt.Fprintln(output, "       pact [--config PATH] sessions list | show SESSION_ID")
		fmt.Fprintln(output, "       pact [--config PATH] resume [SESSION_ID] [--detach]")
		fmt.Fprintln(output, "       pact [--config PATH] daemon | start [--detach] [OPTIONS] | attach SESSION_ID")
		fmt.Fprintln(output, "       pact [--config PATH] approvals [--socket PATH]")
		fmt.Fprintln(output, "       pact [--config PATH] version")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return cfg, nil, err
	}

	if cfg.configPath == "" {
		cfg.configPath, err = config.DefaultHostPath()
		if err != nil {
			return cfg, nil, err
		}
	}

	cfg.configPath, err = filepath.Abs(cfg.configPath)
	return cfg, flags.Args(), err
}

func addNewSessionFlags(flags *flag.FlagSet, cfg *newSessionConfig) {
	flags.BoolVar(&cfg.dind, "dind", false, "Give the agent a dedicated Docker-in-Docker daemon")
	flags.BoolVar(&cfg.hostNet, "hostnet", false, "Run the container on the host's network")
	flags.StringVar(&cfg.workspace, "workspace", "", "Use an existing workspace directory (default: a fresh managed workspace)")
	flags.StringVar(&cfg.workspace, "w", "", "Alias for --workspace")
	flags.StringVar(&cfg.model, "model", "", "Pi model ID or provider/model (default: Pi's model selection)")
	flags.StringVar(&cfg.model, "m", cfg.model, "Alias for --model")
	flags.StringVar(&cfg.effort, "effort", "", "Reasoning effort (omit to use the model default)")
	flags.StringVar(&cfg.effort, "e", cfg.effort, "Alias for --effort")
}

func resolveWorkspace(workspace string) (string, error) {
	if workspace == "" {
		return "", nil
	}

	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}

	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}

	info, err := os.Stat(workspace)
	if err != nil {
		return "", err
	}

	if !info.IsDir() {
		return "", errors.New("workspace must be a directory")
	}

	return workspace, nil
}

func resumeSession(ctx context.Context, args []string, configPath string) error {
	cfg, err := parseResumeArgs(args, os.Stderr)
	if err != nil {
		return err
	}

	return daemon.Resume(ctx, cfg.resumeID, configPath, cfg.detach)
}

func parseResumeArgs(args []string, output io.Writer) (resumeConfig, error) {
	var cfg resumeConfig
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id <= 0 {
			return cfg, fmt.Errorf("session ID must be a positive integer")
		}
		cfg.resumeID = id
		args = args[1:]
	}

	flags := flag.NewFlagSet("pact resume", flag.ContinueOnError)
	flags.SetOutput(output)
	addDetachFlags(flags, &cfg.detach)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact [--config PATH] resume [SESSION_ID] [--detach]")
		fmt.Fprintln(output, "Omit SESSION_ID to resume the last used Pact session.")
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

func exitCode(err error) int {
	if err == nil {
		return 0
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}

	return 1
}
