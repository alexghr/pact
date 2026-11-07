package main

import (
	"context"
	"database/sql"
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

	"github.com/alexghr/pact/internal/session"
	"github.com/alexghr/pact/internal/state"
	"github.com/alexghr/pact/internal/version"
)

const maxSessionTitleLength = 120

type newSessionConfig struct {
	hostNet, dind bool
	workspace     string
	title         string
	model, effort string
}

type runConfig struct {
	newSessionConfig
	configPath string
	prompt     string
}

type resumeConfig struct {
	configPath string
	prompt     string
	resumeID   int64
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
	if len(args) > 0 {
		switch args[0] {
		case "version":
			_, err := fmt.Fprintf(os.Stdout, "pact %s\n", version.Version)
			return err
		case "approvals":
			return serveApprovals(ctx, args[1:])
		case "sessions":
			return manageSessions(ctx, args[1:], os.Stdout, os.Stderr)
		case "resume":
			return resumeSession(ctx, args[1:])
		case "daemon":
			return serveDaemon(ctx, args[1:])
		case "start":
			return startDaemonSession(ctx, args[1:])
		case "attach":
			return attachSession(ctx, args[1:])
		}
	}

	cfg, err := parseArgs(args, os.Stderr)
	if err != nil {
		return err
	}

	sr, err := newSessionRuntime(ctx, nil, cfg.configPath, cfg.newSessionConfig)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Pact session %d\n", sr.session.Record.ID)
	return sr.Run(ctx, cfg.prompt)
}

func parseArgs(args []string, output io.Writer) (runConfig, error) {
	var cfg runConfig

	flags := flag.NewFlagSet("pact", flag.ContinueOnError)
	flags.SetOutput(output)
	addNewSessionFlags(flags, &cfg.newSessionConfig)
	flags.StringVar(&cfg.prompt, "prompt", "", "Prompt to run (omit for an interactive session)")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
	flags.StringVar(&cfg.configPath, "config", "", "Config file (default: $XDG_CONFIG_HOME/pact/config.json or ~/.config/pact/config.json)")

	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact [--workspace PATH] [--prompt TEXT] [--model MODEL] [--effort EFFORT] [--config PATH]")
		fmt.Fprintln(output, "       pact sessions list | show SESSION_ID")
		fmt.Fprintln(output, "       pact resume [SESSION_ID] [--prompt TEXT] [--config PATH]")
		fmt.Fprintln(output, "       pact daemon | start [--detach] [OPTIONS] | attach SESSION_ID")
		fmt.Fprintln(output, "       pact approvals [--socket PATH]")
		fmt.Fprintln(output, "       pact version")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return cfg, err
	}

	cfg.title = sessionTitle(cfg.prompt)

	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	var err error
	cfg.workspace, err = resolveWorkspace(cfg.workspace)
	return cfg, err
}

func addNewSessionFlags(flags *flag.FlagSet, cfg *newSessionConfig) {
	flags.BoolVar(&cfg.dind, "dind", false, "Give the agent a dedicated Docker-in-Docker daemon")
	flags.BoolVar(&cfg.hostNet, "hostnet", false, "Run the container on the host's network")
	flags.StringVar(&cfg.workspace, "workspace", "", "Use an existing workspace directory (default: a fresh managed workspace)")
	flags.StringVar(&cfg.workspace, "w", "", "Alias for --workspace")
	flags.StringVar(&cfg.model, "model", "gpt-6.1-sol", "Model to use")
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

func resumeSession(ctx context.Context, args []string) error {
	cfg, err := parseResumeArgs(args, os.Stderr)
	if err != nil {
		return err
	}

	sr, err := newSessionRuntime(ctx, &cfg.resumeID, cfg.configPath, newSessionConfig{})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Pact resumed session %d\n", sr.session.Record.ID)
	return sr.Run(ctx, cfg.prompt)
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
	flags.StringVar(&cfg.configPath, "config", "", "Config file (default: current host Pact config)")
	flags.StringVar(&cfg.prompt, "prompt", "", "Continue the saved conversation with a prompt")
	flags.StringVar(&cfg.prompt, "p", "", "Alias for --prompt")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact resume [SESSION_ID] [--prompt TEXT] [--config PATH]")
		fmt.Fprintln(output, "Omit SESSION_ID to resume the last used Pact session.")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return cfg, err
	}

	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	promptFlagExists := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "prompt" || f.Name == "p" {
			promptFlagExists = true
		}
	})

	if promptFlagExists && strings.TrimSpace(cfg.prompt) == "" {
		return cfg, fmt.Errorf("--prompt requires nonempty text")
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

type sessionRuntime struct {
	store   *state.Store
	session *session.Session
}

// A nil resumeID starts a new session; zero resumes the last used session.
func newSessionRuntime(ctx context.Context, resumeID *int64, configPath string, cfg newSessionConfig) (sessionRuntime, error) {
	var runtime sessionRuntime
	var err error

	runtime.store, err = session.OpenStore(ctx)
	if err != nil {
		return runtime, err
	}

	if resumeID != nil && *resumeID == 0 {
		last, err := runtime.store.LastSession(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = errors.New("no sessions to resume")
			}
			return sessionRuntime{}, errors.Join(err, runtime.store.Close())
		}
		resumeID = &last.ID
	}

	nix, err := session.PrepareNix(ctx)
	if err != nil {
		return sessionRuntime{}, errors.Join(err, runtime.store.Close())
	}

	if resumeID != nil {
		runtime.session, err = session.Restore(ctx, runtime.store, nix, *resumeID, configPath)
	} else {
		runtime.session, err = session.New(ctx, runtime.store, nix, session.Options{
			Workspace:    cfg.workspace,
			Title:        cfg.title,
			Model:        cfg.model,
			Effort:       cfg.effort,
			ConfigPath:   configPath,
			Capabilities: session.Capabilities{DIND: cfg.dind, HostNetwork: cfg.hostNet},
		})
	}

	if err != nil {
		return sessionRuntime{}, errors.Join(err, runtime.store.Close())
	}

	return runtime, nil
}

func (sr sessionRuntime) Run(ctx context.Context, prompt string) (runErr error) {
	defer func() {
		runErr = errors.Join(runErr, sr.session.Cleanup(), sr.store.Close())
	}()
	return sr.session.Start(ctx, prompt)
}
