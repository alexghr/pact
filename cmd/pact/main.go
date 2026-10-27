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
	"time"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/environment"
	"github.com/alexghr/pact/internal/gitbroker"
	"github.com/alexghr/pact/internal/hostbridge"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/sidecar"
	"github.com/alexghr/pact/internal/state"
)

const maxSessionTitleLength = 120

// agentConfig holds the options passed to the container's agent entrypoint.
// Pact's environment instructions are supplied by the container entrypoint.
type agentConfig struct {
	hostNet     bool
	dind        bool
	workspace   string
	prompt      string
	model       string
	effort      string
	config      string
	git         config.Git
	bridge      hostbridge.Bridge
	mcpNames    []string
	environment state.Environment
	resumeID    int64
	threadID    string
	authFile    string
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
		fmt.Fprintln(output, "       pact environments gc | repair SESSION_ID")
		fmt.Fprintln(output, "       pact workspaces remove SESSION_ID [--yes]")
		fmt.Fprintln(output, "       pact sessions list | show SESSION_ID | threads SESSION_ID")
		fmt.Fprintln(output, "       pact resume SESSION_ID[:THREAD_UUID] [--prompt TEXT] [--config PATH]")
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

func agentContainer(cfg agentConfig, image string, uid, gid int) docker.ContainerConfig {
	config := docker.ContainerConfig{
		Image: image, TTY: cfg.prompt == "", Interactive: cfg.prompt == "",
		Mounts: []docker.Mount{
			{Type: "bind", Source: cfg.workspace, Target: "/home/pact/workspace"},
			{Type: "volume", Source: cfg.environment.StateVolume, Target: "/home/pact/.codex"},
			{Type: "bind", Source: cfg.authFile, Target: "/home/pact/.codex/auth.json", ReadOnly: true},
		},
		Env: []string{"HOST_UID=" + strconv.Itoa(uid), "HOST_GID=" + strconv.Itoa(gid),
			"PACT_GIT_NAME=" + cfg.git.Name, "PACT_GIT_EMAIL=" + cfg.git.Email,
			"PACT_GIT_URL=" + cfg.bridge.AgentURL() + gitbroker.HandlerPath,
			"PACT_EVENTS_URL=" + cfg.bridge.AgentURL() + "/harness/events"},
		Command: []string{cfg.prompt, cfg.model, cfg.effort},
	}
	if cfg.hostNet {
		config.Network = "host"
	}
	environment.ConfigureContainer(cfg.environment, &config)
	if cfg.resumeID > 0 {
		config.Env = append(config.Env, "PACT_RESUME_THREAD="+cfg.threadID)
	}
	bridgeConfig := cfg.bridge.ContainerConfig()
	for _, mount := range bridgeConfig.ReadOnlyMounts {
		config.Mounts = append(config.Mounts, docker.Mount{Type: "bind", Source: mount.Source, Target: mount.Target, ReadOnly: true})
	}
	config.Env = append(config.Env, bridgeConfig.Env...)
	for _, name := range cfg.mcpNames {
		config.Command = append(config.Command, "--config", fmt.Sprintf(
			`mcp_servers.%s={url="%s/mcp/%s"}`, name, cfg.bridge.AgentURL(), name))
	}
	return config
}

func runContainer(ctx context.Context, config docker.ContainerConfig, onStart func() error, consume func(io.Reader) error) (started, stopped bool, runErr error) {
	id, err := docker.CreateContainer(ctx, config)
	if err != nil {
		return false, false, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := docker.RemoveContainer(cleanupCtx, id, true, true)
		stopped = err == nil
		runErr = errors.Join(runErr, err)
	}()
	started, runErr = docker.Attach(ctx, id, docker.AttachOptions{
		Streams:     docker.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		Interactive: consume == nil, Interrupt: true, OnStart: onStart, Consume: consume,
	})
	return
}

func run(args []string) (runErr error) {
	if len(args) > 0 && args[0] == "environments" {
		return manageEnvironments(args[1:])
	}
	if len(args) > 0 && args[0] == "sessions" {
		return manageSessions(args[1:], os.Stdout)
	}
	if len(args) > 0 && args[0] == "workspaces" {
		return manageWorkspaces(args[1:], os.Stdout)
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
	settings, err := config.Load(cfg.config)
	if err != nil {
		return err
	}
	cfg.git = settings.Git
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg.authFile, err = createAuthSnapshot(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return err
	}
	defer os.Remove(cfg.authFile)
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancelCause(signalCtx)
	defer cancel(nil)
	servers, err := mcpServers(settings.MCP)
	if err != nil {
		return err
	}
	for _, server := range servers {
		cfg.mcpNames = append(cfg.mcpNames, strings.TrimPrefix(server.Path, "/mcp/"))
	}
	store, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	var launch state.Launch
	if cfg.resumeID > 0 {
		var saved state.LaunchSettings
		launch, saved, err = store.ResumeLaunch(ctx, cfg.resumeID, cfg.prompt != "")
		cfg.model, cfg.effort, cfg.dind = saved.Model, saved.Effort, saved.DinD
		cfg.hostNet = saved.HostNet
	} else {
		launch, err = store.CreateLaunch(ctx, state.LaunchOptions{
			Workspace: cfg.workspace,
			Title:     sessionTitle(cfg.prompt),
			Owner:     "uid:" + strconv.Itoa(os.Getuid()),
			Backend:   "codex",
			Settings:  state.LaunchSettings{Model: cfg.model, Effort: cfg.effort, Prompted: cfg.prompt != "", DinD: cfg.dind, HostNet: cfg.hostNet},
		})
	}
	if err != nil {
		return err
	}
	cfg.workspace = launch.Workspace
	cfg.environment = launch.Environment
	fmt.Fprintf(os.Stderr, "Pact session %d\n", launch.SessionID)
	containerStarted := false
	runtimeStopped := false
	defer func() {
		status := state.Succeeded
		message := ""
		if runErr != nil {
			message = runErr.Error()
			status = state.StartupFailed
			if containerStarted {
				status = state.Failed
			}
		}
		if signalCtx.Err() != nil {
			status = state.Interrupted
		}
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer finishCancel()
		runErr = errors.Join(runErr, store.FinishExecution(finishCtx, launch.ExecutionID, status, exitCode(runErr), message))
	}()
	broker, err := gitbroker.New(cfg.config, cfg.workspace)
	if err != nil {
		return fmt.Errorf("start Git broker: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, broker.Close()) }()
	servers = append(servers, hostbridge.Server{Path: gitbroker.HandlerPath, Handler: broker})
	servers = append(servers, hostbridge.Server{Path: "/harness/events", Handler: sessionEvents(store, launch.SessionID)})
	bridge, err := hostbridge.Start(servers, cfg.workspace)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, bridge.Close()) }()
	cfg.bridge = bridge
	go func() {
		select {
		case err := <-bridge.Done():
			cancel(errors.Join(errors.New("host bridge stopped"), err))
		case <-ctx.Done():
		}
	}()
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	if err := environment.Prepare(ctx, store, cfg.environment); err != nil {
		return fmt.Errorf("prepare environment for session %d: %w", launch.SessionID, err)
	}
	if cfg.resumeID > 0 {
		if err := refreshSession(ctx, store, launch, image); err != nil {
			return err
		}
		threads, err := store.Threads(ctx, launch.SessionID)
		if err != nil {
			return err
		}
		var thread state.Thread
		if cfg.threadID == "" {
			thread, err = state.LastThread(threads)
		} else {
			thread, err = exactThread(threads, cfg.threadID)
		}
		if err != nil {
			return err
		}
		if thread.ID == "" && cfg.prompt != "" {
			return fmt.Errorf("session %d has no saved conversation to continue", launch.SessionID)
		}
		cfg.threadID = thread.ID
		if thread.ID == "" {
			fmt.Fprintf(os.Stderr, "Starting the first saved conversation in Pact session %d\n", launch.SessionID)
		} else {
			fmt.Fprintf(os.Stderr, "Resuming Pact thread %d:%s\n", launch.SessionID, thread.ID)
		}
	}
	defer func() {
		if !containerStarted || !runtimeStopped {
			return
		}
		refreshCtx, refreshCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer refreshCancel()
		runErr = errors.Join(runErr, refreshSession(refreshCtx, store, launch, image))
	}()
	agent := agentContainer(cfg, image, os.Getuid(), os.Getgid())
	if cfg.dind {
		name, cleanup, err := sidecar.StartDINDSidecar(ctx, cfg.workspace)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, cleanup()) }()
		agent.Mounts = append(agent.Mounts, docker.Mount{Type: "volume", Source: name, Target: sidecar.DINDSocketDir})
		agent.Env = append(agent.Env, "DOCKER_HOST="+sidecar.DINDSocket)
	}
	var consume func(io.Reader) error
	if cfg.prompt != "" {
		consume = func(input io.Reader) error {
			return codex.ConsumeExec(ctx, input, store, launch, os.Stdout, os.Stderr, cfg.threadID)
		}
	}
	containerStarted, runtimeStopped, err = runContainer(ctx, agent, func() error {
		return store.StartExecution(ctx, launch.ExecutionID, cfg.threadID)
	}, consume)
	if err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	return context.Cause(ctx)
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
