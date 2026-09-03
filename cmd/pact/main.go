package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/harness"
	"github.com/alexghr/pact/internal/state"
	"github.com/alexghr/pact/internal/web"
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
		options, resumeTarget, err := prepareRunOptions(ctx, args[1:], os.Stderr)
		if err != nil {
			return err
		}
		return startRun(ctx, options.Options, resumeTarget)
	case "web":
		if len(args) != 1 {
			return fmt.Errorf("usage: pact web")
		}
		return startWeb(ctx)
	case "help", "-h", "--help":
		fmt.Fprint(os.Stdout, usage())
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage())
	}
}

type runOptions struct {
	harness.Options
	resumeSession int64
}

func defaultRunOptions() runOptions {
	return runOptions{Options: harness.DefaultOptions()}
}

func newRunFlagSet(options *runOptions, output io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.Workspace, "dir", options.Workspace, "working directory to mount")
	flags.StringVar(&options.Model, "model", options.Model, "Codex model")
	flags.StringVar(&options.Effort, "effort", options.Effort, "model reasoning effort")
	flags.StringVar(&options.Image, "image", options.Image, "container image profile (generic or go)")
	flags.Int64Var(&options.resumeSession, "resume", options.resumeSession, "Pact session ID to resume")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: pact run [options] PROMPT")
		flags.PrintDefaults()
	}
	return flags
}

func resumeSessionID(args []string, output io.Writer) (int64, error) {
	options := defaultRunOptions()
	flags := newRunFlagSet(&options, output)
	if err := flags.Parse(args); err != nil {
		return 0, err
	}
	return options.resumeSession, nil
}

func parseRunOptions(args []string, output io.Writer) (runOptions, error) {
	return parseRunOptionsFrom(args, output, defaultRunOptions())
}

func parseRunOptionsFrom(args []string, output io.Writer, options runOptions) (runOptions, error) {
	flags := newRunFlagSet(&options, output)
	if err := flags.Parse(args); err != nil {
		return runOptions{}, err
	}
	if flags.NArg() != 1 {
		return runOptions{}, fmt.Errorf("usage: pact run [options] PROMPT")
	}
	options.Prompt = flags.Arg(0)
	if options.Prompt == "" {
		return runOptions{}, fmt.Errorf("prompt must not be empty")
	}
	if options.Image != "generic" && options.Image != "go" {
		return runOptions{}, fmt.Errorf("unsupported image %q (must be generic or go)", options.Image)
	}
	return options, nil
}

func prepareRunOptions(ctx context.Context, args []string, output io.Writer) (runOptions, *state.ResumeTarget, error) {
	sessionID, err := resumeSessionID(args, output)
	if err != nil {
		return runOptions{}, nil, err
	}
	if sessionID == 0 {
		options, err := parseRunOptions(args, output)
		return options, nil, err
	}

	store, _, err := openStore(ctx)
	if err != nil {
		return runOptions{}, nil, err
	}
	target, targetErr := store.GetResumeTarget(ctx, sessionID)
	if err := errors.Join(targetErr, store.Close()); err != nil {
		return runOptions{}, nil, err
	}

	defaults := defaultRunOptions()
	defaults.Model = target.Model
	defaults.Effort = target.Effort
	defaults.Image = target.DockerfileVariant
	defaults.resumeSession = target.PactSessionID
	options, err := parseRunOptionsFrom(args, output, defaults)
	if err != nil {
		return runOptions{}, nil, err
	}
	return options, &target, nil
}

func startRun(ctx context.Context, options harness.Options, resumeTarget *state.ResumeTarget) error {
	store, home, err := openStore(ctx)
	if err != nil {
		return err
	}
	runner := harness.New(store, home, docker.NewCLI(os.Stdin, os.Stdout, os.Stderr), os.Stdout)
	var sessionID int64
	if resumeTarget == nil {
		sessionID, options.Workspace, err = runner.CreateSession(ctx, options.Workspace)
		if err != nil {
			return errors.Join(err, store.Close())
		}
	} else {
		sessionID = resumeTarget.PactSessionID
	}
	fmt.Fprintf(os.Stderr, "Pact session %d\n", sessionID)
	_, runErr := runner.Run(ctx, sessionID, options, resumeTarget)
	return errors.Join(runErr, store.Close())
}

func startWeb(ctx context.Context) error {
	store, home, err := openStore(ctx)
	if err != nil {
		return err
	}
	runner := harness.New(store, home, docker.NewCLI(nil, os.Stdout, os.Stderr), io.Discard)
	server, err := web.New(ctx, store, runner)
	if err != nil {
		return errors.Join(err, store.Close())
	}
	return errors.Join(server.ListenAndServe(os.Stderr), store.Close())
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
	fmt.Fprintln(w, "ID\tSESSION\tSTATUS\tWORKSPACE\tMODEL\tEFFORT\tIMAGE")
	for _, run := range runs {
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			run.ID,
			run.PactSessionID,
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

func usageError() error {
	return errors.New(usage())
}

func usage() string {
	return "Usage:\n  pact list\n  pact run [options] PROMPT\n  pact web\n"
}
