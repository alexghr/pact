package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexghr/pact/internal/artifacts"
	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/imagebuilder"
	"github.com/alexghr/pact/internal/state"
)

const containerWorkspace = "/home/pact/workspace"

type Options struct {
	Workspace string
	Prompt    string
	Model     string
	Effort    string
	Image     string
}

type SessionOptions struct {
	Workspace     string
	RepositoryIDs []int64
}

func DefaultOptions() Options {
	return Options{
		Workspace: ".",
		Model:     "gpt-5.6-sol",
		Effort:    "low",
		Image:     "generic",
	}
}

type Runner struct {
	store       *state.Store
	authFile    string
	storageRoot string
	engine      docker.ContainerRunner
	images      imagebuilder.Resolver
	checkout    repositoryCheckout
	output      io.Writer
}

func New(store *state.Store, authFile, storageRoot string, engine docker.Engine, output io.Writer) *Runner {
	return NewWithImageResolver(
		store,
		authFile,
		storageRoot,
		engine,
		output,
		imagebuilder.NewOnDemand(engine, imagebuilder.BuiltinProfiles()),
	)
}

func NewWithImageResolver(
	store *state.Store,
	authFile, storageRoot string,
	engine docker.ContainerRunner,
	output io.Writer,
	images imagebuilder.Resolver,
) *Runner {
	return &Runner{
		store:       store,
		authFile:    authFile,
		storageRoot: storageRoot,
		engine:      engine,
		images:      images,
		checkout:    hostGitCheckout{},
		output:      output,
	}
}

func (r *Runner) CreateSession(ctx context.Context, options SessionOptions) (int64, string, error) {
	if options.Workspace == "" {
		return r.createManagedSession(ctx, options.RepositoryIDs)
	}
	if len(options.RepositoryIDs) != 0 {
		return 0, "", errors.New("repository checkouts require a managed workspace")
	}
	workspace, err := canonicalWorkspace(options.Workspace)
	if err != nil {
		return 0, "", err
	}
	sessionID, err := r.store.CreateSession(ctx, workspace)
	if err != nil {
		return 0, "", err
	}
	return sessionID, workspace, nil
}

func (r *Runner) Run(
	ctx context.Context,
	pactSessionID int64,
	options Options,
	resumeTarget *state.ResumeTarget,
) (int64, error) {
	if options.Prompt == "" {
		return 0, errors.New("prompt must not be empty")
	}
	if r.images == nil || !r.images.HasProfile(options.Image) {
		return 0, fmt.Errorf("unsupported image %q", options.Image)
	}
	workspace, err := canonicalWorkspace(options.Workspace)
	if err != nil {
		return 0, err
	}
	session, err := r.store.GetSession(ctx, pactSessionID)
	if err != nil {
		return 0, err
	}
	if session.WorkspaceDir != workspace {
		return 0, fmt.Errorf(
			"session %d belongs to workspace %q, not selected workspace %q",
			pactSessionID,
			session.WorkspaceDir,
			workspace,
		)
	}
	if err := validateResumeTarget(pactSessionID, workspace, resumeTarget); err != nil {
		return 0, err
	}

	image, err := r.images.Resolve(ctx, options.Image)
	if err != nil {
		return 0, err
	}
	artifactBroker, err := artifacts.StartBroker(ctx, r.store, pactSessionID)
	if err != nil {
		return 0, err
	}

	runID, err := r.store.StartRun(ctx, state.Run{
		PactSessionID:     pactSessionID,
		Model:             options.Model,
		Effort:            options.Effort,
		DockerfileVariant: options.Image,
	})
	if err != nil {
		return 0, errors.Join(err, artifactBroker.Close())
	}
	var stateVolume string
	if resumeTarget != nil {
		stateVolume = resumeTarget.StateVolume
	} else {
		stateVolume = fmt.Sprintf("pact-codex-state-%d-%d", pactSessionID, runID)
	}

	runErr := r.engine.Run(ctx, docker.RunOptions{
		Image: image,
		Env: []string{
			"HOST_UID=" + strconv.Itoa(os.Getuid()),
			"HOST_GID=" + strconv.Itoa(os.Getgid()),
		},
		Volumes: []string{
			workspace + ":" + containerWorkspace,
			stateVolume + ":/home/pact/.codex",
			r.authFile + ":/opt/pact/host-auth.json:ro",
			artifactBroker.Mount(),
		},
	}, func(ctx context.Context, reader io.Reader, writer io.Writer) error {
		return r.runCodexTurn(ctx, reader, writer, options, runID, stateVolume, resumeTarget)
	})
	runErr = errors.Join(runErr, artifactBroker.Close())

	status := "finished"
	exitCode := 0
	var storedExitCode *int = &exitCode
	if runErr != nil {
		status = "error"
		storedExitCode = processExitCode(runErr)
	}
	completeErr := r.store.CompleteRun(ctx, runID, status, storedExitCode)
	return runID, errors.Join(runErr, completeErr)
}

func (r *Runner) runCodexTurn(
	ctx context.Context,
	reader io.Reader,
	writer io.Writer,
	options Options,
	runID int64,
	stateVolume string,
	resumeTarget *state.ResumeTarget,
) error {
	client := codex.NewClient(reader, writer)
	initialized, err := client.Initialize(ctx, codex.ClientInfo{
		Name:    "pact",
		Title:   "Pact",
		Version: "0.1.0",
	})
	if err != nil {
		return err
	}

	var thread codex.Thread
	if resumeTarget == nil {
		thread, err = client.StartThread(ctx, codex.ThreadOptions{
			Model:          options.Model,
			CWD:            containerWorkspace,
			ApprovalPolicy: codex.ApprovalNever,
			ServiceName:    "pact",
		})
	} else {
		thread, err = client.ResumeThread(ctx, resumeTarget.ThreadID, codex.ThreadResumeOptions{
			Model:          options.Model,
			CWD:            containerWorkspace,
			ApprovalPolicy: codex.ApprovalNever,
		})
	}
	if err != nil {
		return err
	}
	if err := r.store.LinkCodexThread(ctx, runID, state.CodexThread{
		ThreadID:    thread.ID,
		SessionID:   thread.SessionID,
		UserAgent:   initialized.UserAgent,
		StateVolume: stateVolume,
	}); err != nil {
		return err
	}
	turn, err := client.StartTurn(ctx, thread.ID, options.Prompt, codex.TurnOptions{
		Model:          options.Model,
		Effort:         options.Effort,
		CWD:            containerWorkspace,
		ApprovalPolicy: codex.ApprovalNever,
		SandboxPolicy: &codex.SandboxPolicy{
			Type:          codex.SandboxExternal,
			NetworkAccess: codex.NetworkEnabled,
		},
	})
	if err != nil {
		return err
	}

	waitErr := waitForCodexTurn(
		ctx,
		client,
		r.output,
		thread.ID,
		turn.ID,
		func(sequence int64, message codex.Message) error {
			return r.store.AppendCodexEvent(ctx, runID, sequence, message.Method, message.Params)
		},
	)
	transcript, readErr := client.ReadThread(ctx, thread.ID)
	var storeErr error
	if readErr == nil {
		storeErr = r.store.StoreCodexTranscript(ctx, runID, transcript)
	}
	return errors.Join(waitErr, readErr, storeErr)
}

func canonicalWorkspace(path string) (string, error) {
	workspace, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return "", fmt.Errorf("inspect working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory %q is not a directory", workspace)
	}
	if strings.Contains(workspace, ":") {
		return "", fmt.Errorf("working directory %q contains ':' and cannot be mounted safely", workspace)
	}
	return workspace, nil
}

func validateResumeTarget(pactSessionID int64, workspace string, target *state.ResumeTarget) error {
	if target == nil {
		return nil
	}
	if target.PactSessionID != pactSessionID {
		return fmt.Errorf(
			"resume target belongs to session %d, not session %d",
			target.PactSessionID,
			pactSessionID,
		)
	}
	if target.WorkspaceDir != workspace {
		return fmt.Errorf(
			"resume session %d belongs to workspace %q, not selected workspace %q",
			target.PactSessionID,
			target.WorkspaceDir,
			workspace,
		)
	}
	return nil
}

func waitForCodexTurn(
	ctx context.Context,
	client *codex.Client,
	output io.Writer,
	threadID string,
	turnID string,
	recordEvent func(int64, codex.Message) error,
) error {
	var sequence int64
	for {
		message, err := client.NextMessage(ctx)
		if err != nil {
			return err
		}
		if recordEvent != nil && shouldRecordCodexEvent(message) {
			sequence++
			if err := recordEvent(sequence, message); err != nil {
				return err
			}
		}
		if message.Method != "" && len(message.ID) != 0 {
			return fmt.Errorf("unsupported app-server request %q", message.Method)
		}

		switch message.Method {
		case "item/agentMessage/delta":
			var delta struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
				Delta    string `json:"delta"`
			}
			if err := json.Unmarshal(message.Params, &delta); err != nil {
				return fmt.Errorf("decode %s: %w", message.Method, err)
			}
			if delta.ThreadID == threadID && delta.TurnID == turnID {
				if _, err := io.WriteString(output, delta.Delta); err != nil {
					return fmt.Errorf("write agent message: %w", err)
				}
			}

		case "turn/completed":
			var completed struct {
				ThreadID string     `json:"threadId"`
				Turn     codex.Turn `json:"turn"`
			}
			if err := json.Unmarshal(message.Params, &completed); err != nil {
				return fmt.Errorf("decode %s: %w", message.Method, err)
			}
			if completed.ThreadID != threadID || completed.Turn.ID != turnID {
				continue
			}
			switch completed.Turn.Status {
			case "completed":
				return nil
			case "failed":
				if completed.Turn.Error != nil && completed.Turn.Error.Message != "" {
					return fmt.Errorf("codex turn failed: %s", completed.Turn.Error.Message)
				}
				return errors.New("codex turn failed")
			case "interrupted":
				return errors.New("codex turn interrupted")
			default:
				return fmt.Errorf("codex turn completed with unexpected status %q", completed.Turn.Status)
			}
		}
	}
}

func shouldRecordCodexEvent(message codex.Message) bool {
	if len(message.ID) != 0 && message.Method != "" {
		return true
	}
	if strings.HasPrefix(message.Method, "model/") {
		return true
	}
	switch message.Method {
	case "thread/started",
		"thread/tokenUsage/updated",
		"turn/started",
		"turn/completed",
		"item/started",
		"item/completed",
		"warning",
		"configWarning",
		"error":
		return true
	default:
		return false
	}
}

func processExitCode(err error) *int {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return nil
	}
	code := exitError.ExitCode()
	return &code
}
