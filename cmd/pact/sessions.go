package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/alexghr/pact/internal/state"
)

func manageSessions(args []string, output io.Writer) (runErr error) {
	if len(args) == 0 {
		return errors.New("usage: pact sessions list [--workspace PATH | --all] [--json] | show ID [--json]")
	}
	operation := args[0]
	args = args[1:]
	var id int64
	if operation == "show" {
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
		flags.StringVar(&workspace, "workspace", "", "Filter by host workspace (default: all workspaces)")
		flags.BoolVar(&all, "all", false, "List sessions from all workspaces (the default)")
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
	if operation == "list" && workspace != "" {
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
			return json.NewEncoder(output).Encode(sessions)
		}
		writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "SESSION\tSTATUS\tUPDATED\tTITLE")
		for _, session := range sessions {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n", session.ID, session.Status,
				time.UnixMilli(session.UpdatedAt).UTC().Format(time.RFC3339), strings.Join(strings.Fields(session.Title), " "))
		}
		return writer.Flush()
	}
	session, err := store.Session(ctx, id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(output).Encode(session)
	}
	fmt.Fprintf(output, "Session: %d\nTitle: %s\nUpdated: %s\nWorkspace: %s\nBackend: %s\nBackend ref: %s\nStatus: %s\n",
		session.ID, session.Title, time.UnixMilli(session.UpdatedAt).UTC().Format(time.RFC3339), session.Workspace, session.Backend, session.BackendRef, session.Status)
	fmt.Fprintf(output, "Workspace managed: %t\nWorkspace status: %s\n", session.WorkspaceManaged, session.WorkspaceStatus)
	if session.Error != "" {
		fmt.Fprintln(output, "Last error:", session.Error)
	}
	return nil
}
