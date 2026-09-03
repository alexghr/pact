package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/state"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) == 0 {
		return usageError()
	}

	ctx := context.Background()
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: pact list")
		}
		return listRuns(ctx, os.Stdout)
	case "run":
		options, err := parseRunOptions(args[1:], os.Stderr)
		if err != nil {
			return err
		}
		return startRun(ctx, options)
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usage())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage())
	}
}

type runOptions struct {
	workspace string
	prompt    string
	model     string
	effort    string
	image     string
}

func parseRunOptions(args []string, output io.Writer) (runOptions, error) {
	options := runOptions{}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.workspace, "dir", ".", "working directory to mount")
	flags.StringVar(&options.model, "model", "gpt-5.6-sol", "Codex model")
	flags.StringVar(&options.effort, "effort", "low", "model reasoning effort")
	flags.StringVar(&options.image, "image", "generic", "container image profile (generic or go)")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact run [options] PROMPT")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return runOptions{}, err
	}
	if flags.NArg() != 1 {
		return runOptions{}, fmt.Errorf("usage: pact run [options] PROMPT")
	}
	options.prompt = flags.Arg(0)
	if options.prompt == "" {
		return runOptions{}, fmt.Errorf("prompt must not be empty")
	}
	if options.image != "generic" && options.image != "go" {
		return runOptions{}, fmt.Errorf("unsupported image %q (must be generic or go)", options.image)
	}
	return options, nil
}

func startRun(ctx context.Context, options runOptions) error {
	workspace, err := canonicalWorkspace(options.workspace)
	if err != nil {
		return err
	}

	image := "pact-codex:" + options.image
	var engine docker.Engine = docker.NewCLI(os.Stdin, os.Stdout, os.Stderr)
	if err := engine.Build(ctx, docker.BuildOptions{
		ContextDir: "./container",
		Dockerfile: filepath.Join("container", options.image+".Dockerfile"),
		Tag:        image,
	}); err != nil {
		return err
	}

	store, home, err := openStore(ctx)
	if err != nil {
		return err
	}

	runID, err := store.StartRun(ctx, state.Run{
		WorkspaceDir:      workspace,
		Model:             options.model,
		Effort:            options.effort,
		DockerfileVariant: options.image,
	})
	if err != nil {
		return errors.Join(err, store.Close())
	}

	runErr := engine.Run(ctx, docker.RunOptions{
		Image: image,
		Args:  []string{options.prompt, options.model, options.effort},
		Env: []string{
			"HOST_UID=" + strconv.Itoa(os.Getuid()),
			"HOST_GID=" + strconv.Itoa(os.Getgid()),
		},
		Volumes: []string{
			workspace + ":/home/pact/workspace",
			"pact-codex-state:/home/pact/.codex",
			filepath.Join(home, ".codex/auth.json") + ":/opt/pact/host-auth.json:ro",
		},
	})

	status := "finished"
	exitCode := 0
	var storedExitCode *int = &exitCode
	if runErr != nil {
		status = "error"
		storedExitCode = processExitCode(runErr)
	}
	completeErr := store.CompleteRun(ctx, runID, status, storedExitCode)
	closeErr := store.Close()
	return errors.Join(runErr, completeErr, closeErr)
}

func listRuns(ctx context.Context, output io.Writer) error {
	store, _, err := openStore(ctx)
	if err != nil {
		return err
	}
	runs, listErr := store.ListRuns(ctx)
	closeErr := store.Close()
	if err := errors.Join(listErr, closeErr); err != nil {
		return err
	}
	return writeRunList(output, runs)
}

func writeRunList(output io.Writer, runs []state.RunRecord) error {
	w := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tWORKSPACE\tMODEL\tEFFORT\tIMAGE")
	for _, run := range runs {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			run.ID,
			run.Status,
			run.WorkspaceDir,
			run.Model,
			run.Effort,
			run.DockerfileVariant,
		)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write run list: %w", err)
	}
	return nil
}

func openStore(ctx context.Context) (*state.Store, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("find home directory: %w", err)
	}
	store, err := state.Open(ctx, filepath.Join(home, ".local", "state", "pact", "pact.db"))
	if err != nil {
		return nil, "", err
	}
	return store, home, nil
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

func usageError() error {
	return errors.New(usage())
}

func usage() string {
	return "Usage:\n  pact list\n  pact run [options] PROMPT\n"
}

func processExitCode(err error) *int {
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return nil
	}
	code := exitError.ExitCode()
	return &code
}
