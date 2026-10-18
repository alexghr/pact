package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/alexghr/pact/internal/state"
)

func manageSessions(args []string) (runErr error) {
	if len(args) == 0 {
		return errors.New("usage: pact sessions list [--workspace PATH | --all] [--json] | show ID [--json] | threads ID [--json]")
	}
	operation := args[0]
	args = args[1:]
	var id int64
	if operation == "show" || operation == "threads" {
		if len(args) == 0 {
			return errors.New("a positive session ID is required")
		}
		id, _ = strconv.ParseInt(args[0], 10, 64)
		if id <= 0 {
			return errors.New("a positive session ID is required")
		}
		args = args[1:]
	} else if operation != "list" {
		return fmt.Errorf("unknown sessions command %q", operation)
	}
	flags := flag.NewFlagSet("pact sessions "+operation, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	jsonOutput := flags.Bool("json", false, "Print machine-readable JSON")
	var workspace string
	var all bool
	if operation == "list" {
		flags.StringVar(&workspace, "workspace", "", "Filter by host workspace (default: current directory)")
		flags.BoolVar(&all, "all", false, "List sessions from all workspaces")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	if all && workspace != "" {
		return errors.New("--workspace and --all cannot be combined")
	}
	if operation == "list" && !all {
		if workspace == "" {
			workspace = "."
		}
		var err error
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			return err
		}
		workspace, err = filepath.EvalSymlinks(workspace)
		if err != nil {
			return fmt.Errorf("resolve workspace: %w", err)
		}
	}
	ctx := context.Background()
	store, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	if operation == "list" {
		sessions, err := store.Sessions(ctx, workspace)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(sessions)
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "SESSION\tSTATUS\tWORKSPACE")
		for _, session := range sessions {
			fmt.Fprintf(writer, "%d\t%s\t%s\n", session.ID, session.Status, session.Workspace)
		}
		return writer.Flush()
	}
	session, err := store.Session(ctx, id)
	if err != nil {
		return err
	}
	threads, err := store.Threads(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		if operation == "threads" {
			return json.NewEncoder(os.Stdout).Encode(threads)
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			state.Session
			Threads []state.Thread `json:"threads"`
		}{session, threads})
	}
	if operation == "show" {
		fmt.Fprintf(os.Stdout, "Session: %d\nWorkspace: %s\nBackend: %s\nStatus: %s\n", session.ID, session.Workspace, session.Backend, session.Status)
		if session.Error != "" {
			fmt.Fprintln(os.Stdout, "Last error:", session.Error)
		}
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "THREAD\tLAST USED\tSTATE\tTITLE")
	for _, thread := range threads {
		status := "available"
		if thread.Internal {
			status = "internal"
		}
		if thread.Archived {
			status = "archived"
		}
		if thread.Missing {
			status = "missing"
		}
		lastUsed := time.Unix(0, thread.LastUsed).UTC().Format(time.RFC3339Nano)
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", thread.ID, lastUsed, status, thread.Title)
	}
	return writer.Flush()
}
