package main

import (
	"context"
	"crypto/rand"
	"encoding/csv"
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

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/dind"
	"github.com/alexghr/pact/internal/environment"
	"github.com/alexghr/pact/internal/gitbroker"
	"github.com/alexghr/pact/internal/hostbridge"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

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
}

func parseArgs(args []string, output io.Writer) (agentConfig, error) {
	cfg := agentConfig{model: "gpt-6.1-sol"}
	flags := flag.NewFlagSet("pact", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.dind, "dind", false, "Give the agent a dedicated Docker-in-Docker daemon")
	flags.BoolVar(&cfg.hostNet, "hostnet", false, "Run the container on the host's network")
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
		fmt.Fprintln(output, "       pact environments gc | repair SESSION_ID")
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

func readOnlyMount(source, target string) string {
	// Docker's --mount uses CSV, so quote paths that contain commas.
	var mount strings.Builder
	writer := csv.NewWriter(&mount)
	_ = writer.Write([]string{"type=bind", "source=" + source, "target=" + target, "readonly"})
	writer.Flush()
	return strings.TrimSuffix(mount.String(), "\n")
}

func dockerArgs(cfg agentConfig, image, home string, uid, gid int, runtimeOptions ...string) []string {
	args := []string{"run", "--rm"}
	if cfg.prompt == "" {
		args = append(args, "-it")
	}
	if cfg.hostNet {
		args = append(args, "--network", "host")
	}
	args = append(args,
		"--volume", cfg.workspace+":/home/pact/workspace",
		"--volume", cfg.environment.StateVolume+":/home/pact/.codex",
		"--volume", filepath.Join(home, ".codex", "auth.json")+":/opt/pact/host-auth.json:ro",
		"--env", "HOST_UID="+strconv.Itoa(uid),
		"--env", "HOST_GID="+strconv.Itoa(gid),
	)
	args = append(args, environment.Mounts(cfg.environment)...)
	if cfg.bridge != nil {
		bridgeConfig := cfg.bridge.ContainerConfig()
		for _, mount := range bridgeConfig.ReadOnlyMounts {
			args = append(args, "--mount", readOnlyMount(mount.Source, mount.Target))
		}
		for _, env := range bridgeConfig.Env {
			args = append(args, "--env", env)
		}
	}
	args = append(args,
		"--env", "PACT_GIT_NAME="+cfg.git.Name,
		"--env", "PACT_GIT_EMAIL="+cfg.git.Email,
		"--env", "PACT_GIT_URL="+cfg.bridge.AgentURL()+"/mcp/"+gitbroker.ServerName,
	)
	args = append(args, runtimeOptions...)
	args = append(args, image, cfg.prompt, cfg.model, cfg.effort)
	for _, name := range cfg.mcpNames {
		args = append(args, "--config", fmt.Sprintf(
			`mcp_servers.%s={url="%s/mcp/%s"}`, name, cfg.bridge.AgentURL(), name,
		))
	}

	return args
}

func runContainer(ctx context.Context, args []string, onStart func() error) (bool, error) {
	// A unique name lets us remove our container if the attached Docker client
	// is interrupted or fails before --rm has had a chance to clean it up.
	name := "pact-" + strings.ToLower(rand.Text())
	args = append([]string{"run", "--name", name}, args[1:]...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Start()
	started := err == nil
	if started {
		if err = onStart(); err != nil {
			_ = cmd.Process.Kill()
		}
		err = errors.Join(err, cmd.Wait())
	}
	// Docker reserves these codes for failure to start the container command.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() >= 125 && exitErr.ExitCode() <= 127 {
		started = false
	}
	if cmd.Process != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Removal also runs after the client has exited, covering cancellation
		// during creation or an interactive detach. An already removed/nonexistent
		// container is normal.
		_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", name).Run()
	}
	return started, err
}

func run(args []string) (runErr error) {
	if len(args) > 0 && args[0] == "environments" {
		return manageEnvironments(args[1:])
	}
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
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancelCause(signalCtx)
	defer cancel(nil)
	servers, err := mcpServers(settings.MCP)
	if err != nil {
		return err
	}
	for _, server := range servers {
		cfg.mcpNames = append(cfg.mcpNames, server.Name)
	}
	store, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	unlock, err := store.LockStorage(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	launch, err := store.CreateLaunch(ctx, state.LaunchOptions{
		Workspace: cfg.workspace,
		Owner:     "uid:" + strconv.Itoa(os.Getuid()),
		Backend:   "codex",
		Settings:  state.LaunchSettings{Model: cfg.model, Effort: cfg.effort, Prompted: cfg.prompt != "", DinD: cfg.dind},
	})
	if err != nil {
		return err
	}
	cfg.workspace = launch.Workspace
	cfg.environment = launch.Environment
	fmt.Fprintf(os.Stderr, "Pact session %d\n", launch.SessionID)
	containerStarted := false
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
	servers = append(servers, hostbridge.Server{Name: gitbroker.ServerName, Handler: broker})
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
	unlock()
	unlock = nil
	var runtimeOptions []string
	if cfg.dind {
		name, cleanup, err := dind.Start(ctx, cfg.workspace, cfg.environment.StateVolume)
		if err != nil {
			return err
		}
		defer cleanup()
		runtimeOptions = []string{"--volume", name + ":" + dind.SocketDir, "--env", "DOCKER_HOST=" + dind.Socket}
	}
	containerStarted, err = runContainer(ctx, dockerArgs(cfg, image, home, os.Getuid(), os.Getgid(), runtimeOptions...), func() error {
		return store.StartExecution(ctx, launch.ExecutionID)
	})
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

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCode(err))
	}
}
