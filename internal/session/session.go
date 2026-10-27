// Package session owns a Pact session's persistent resources and foreground run.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/gitbroker"
	"github.com/alexghr/pact/internal/hostbridge"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

type Capabilities = state.Capabilities

type Options struct {
	Workspace    string
	Title        string
	Backend      string
	Model        string
	Effort       string
	ConfigPath   string
	Capabilities Capabilities
}

// Session represents one foreground use of retained Pact state. Cleanup removes
// temporary run resources; restoring creates another run using the same state.
type Session struct {
	Record                       state.Session
	store                        *state.Store
	nix                          Nix
	configPath                   string
	bridge                       hostbridge.Bridge
	broker                       *gitbroker.Broker
	authFile, containerID        string
	dindCleanup                  func() error
	cancel                       context.CancelCauseFunc
	started, interrupted, closed bool
	runErr, cleanupErr           error
}

func New(ctx context.Context, store *state.Store, nix Nix, options Options) (*Session, error) {
	record, err := createRecord(ctx, store, options)
	if err != nil {
		return nil, err
	}
	return prepare(ctx, store, nix, record, options.ConfigPath)
}

func Restore(ctx context.Context, store *state.Store, nix Nix, id int64, configPath string) (*Session, error) {
	record, err := restoreRecord(ctx, store, id)
	if err != nil {
		return nil, err
	}
	return prepare(ctx, store, nix, record, configPath)
}

func prepare(ctx context.Context, store *state.Store, nix Nix, record state.Session, configPath string) (*Session, error) {
	s := &Session{store: store, Record: record, nix: nix, configPath: configPath}
	if err := prepareStorage(ctx, store, record.Storage, nix); err != nil {
		s.runErr = err
		return nil, fmt.Errorf("prepare session %d: %w", record.ID, errors.Join(err, s.Cleanup()))
	}
	s.Record.Storage.Provisioned = true
	return s, nil
}

func (s *Session) Start(ctx context.Context, prompt string) (runErr error) {
	if s.closed || s.containerID != "" {
		return errors.New("session run has already started or ended")
	}
	parent := ctx
	ctx, s.cancel = context.WithCancelCause(ctx)
	defer func() {
		s.interrupted = parent.Err() != nil
		runErr = errors.Join(runErr, context.Cause(ctx))
		s.runErr = runErr
	}()
	settings, err := config.Load(s.configPath)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	s.authFile, err = codex.CreateAuthSnapshot(filepath.Join(home, ".codex", "auth.json"))
	if err != nil {
		return err
	}
	servers, err := mcpServers(settings.MCP)
	if err != nil {
		return err
	}
	s.broker, err = gitbroker.New(s.configPath, s.Record.Workspace)
	if err != nil {
		return err
	}
	servers = append(servers, hostbridge.Server{Path: gitbroker.HandlerPath, Handler: s.broker},
		hostbridge.Server{Path: "/harness/binding", Handler: sessionBinding(s.store, s.Record.ID)})
	s.bridge, err = hostbridge.Start(servers, s.Record.Workspace)
	if err != nil {
		return err
	}
	go func() {
		select {
		case err := <-s.bridge.Done():
			s.cancel(errors.Join(errors.New("host bridge stopped"), err))
		case <-ctx.Done():
		}
	}()
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		return err
	}
	uid, gid := strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	agent := docker.ContainerConfig{Image: image,
		Mounts: []docker.Mount{
			{Type: "bind", Source: s.Record.Workspace, Target: "/home/pact/workspace"},
			{Type: "volume", Source: s.Record.Storage.StateVolume, Target: codex.StateDir},
			{Type: "bind", Source: s.authFile, Target: codex.StateDir + "/auth.json", ReadOnly: true},
		},
		Env: []string{"HOST_UID=" + uid, "HOST_GID=" + gid,
			"PACT_GIT_NAME=" + settings.Git.Name, "PACT_GIT_EMAIL=" + settings.Git.Email,
			"PACT_GIT_URL=" + s.bridge.AgentURL() + gitbroker.HandlerPath,
			"PACT_BINDING_URL=" + s.bridge.AgentURL() + "/harness/binding"},
	}
	configureStorage(s.Record.Storage, s.nix, &agent)
	bridgeConfig := s.bridge.ContainerConfig()
	for _, mount := range bridgeConfig.ReadOnlyMounts {
		agent.Mounts = append(agent.Mounts, docker.Mount{Type: "bind", Source: mount.Source, Target: mount.Target, ReadOnly: true})
	}
	agent.Env = append(agent.Env, bridgeConfig.Env...)
	capabilities := s.Record.Settings.Capabilities
	if capabilities.HostNetwork {
		agent.Network = "host"
	}
	if capabilities.DIND {
		var volume string
		volume, s.dindCleanup, err = startDIND(ctx, s.Record.Workspace)
		if err != nil {
			s.dindCleanup = nil
			return err
		}
		agent.Mounts = append(agent.Mounts, docker.Mount{Type: "volume", Source: volume, Target: dindSocketDir})
		agent.Env = append(agent.Env, "DOCKER_HOST="+dindSocket)
	}
	mcp := make(map[string]string, len(settings.MCP))
	for name := range settings.MCP {
		mcp[name] = s.bridge.AgentURL() + "/mcp/" + name
	}
	codex.ConfigureContainer(&agent, codex.Options{Prompt: prompt, Model: s.Record.Settings.Model,
		Effort: s.Record.Settings.Effort, BackendRef: s.Record.BackendRef, MCP: mcp})
	var consume func(io.Reader) error
	if prompt != "" {
		consume = func(input io.Reader) error {
			return codex.ConsumeExec(input, s.bind, os.Stdout, os.Stderr, s.Record.BackendRef)
		}
	}
	return s.runContainer(ctx, agent, consume)
}

func (s *Session) bind(ref string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.store.BindBackendRef(ctx, s.Record.ID, ref)
}

func (s *Session) runContainer(ctx context.Context, config docker.ContainerConfig, consume func(io.Reader) error) error {
	var err error
	s.containerID, err = docker.CreateContainer(ctx, config)
	if err != nil {
		return err
	}
	s.started, err = docker.Attach(ctx, s.containerID, docker.AttachOptions{
		Streams:     docker.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		Interactive: consume == nil, Interrupt: true, Consume: consume,
		OnStart: func() error { return s.store.StartExecution(ctx, s.Record.ExecutionID) },
	})
	return err
}

// Cleanup releases temporary resources and records the outcome. Persistent
// workspace files, agent state, Nix roots, and the shared service are retained.
func (s *Session) Cleanup() error {
	if s.closed {
		return s.cleanupErr
	}
	s.closed = true
	if s.cancel != nil {
		s.cancel(nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if s.containerID != "" {
		s.cleanupErr = docker.RemoveContainer(ctx, s.containerID, true, true)
	}
	if s.dindCleanup != nil {
		s.cleanupErr = errors.Join(s.cleanupErr, s.dindCleanup())
	}
	if s.bridge != nil {
		s.cleanupErr = errors.Join(s.cleanupErr, s.bridge.Close())
	}
	if s.broker != nil {
		s.cleanupErr = errors.Join(s.cleanupErr, s.broker.Close())
	}
	if s.authFile != "" {
		s.cleanupErr = errors.Join(s.cleanupErr, os.Remove(s.authFile))
	}
	outcome := errors.Join(s.runErr, s.cleanupErr)
	status := state.Succeeded
	if outcome != nil {
		status = state.StartupFailed
		if s.started {
			status = state.Failed
		}
	}
	if s.interrupted {
		status = state.Interrupted
	}
	message := ""
	if outcome != nil {
		message = outcome.Error()
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finishCancel()
	s.cleanupErr = errors.Join(s.cleanupErr, s.store.FinishExecution(finishCtx, s.Record.ExecutionID, status, exitCode(outcome), message))
	return s.cleanupErr
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() > 0 {
		return exit.ExitCode()
	}
	return 1
}
