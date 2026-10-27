package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/alexghr/pact/internal/state"
)

func manageWorkspaces(args []string, output io.Writer) (runErr error) {
	if len(args) < 2 || args[0] != "remove" {
		return errors.New("usage: pact workspaces remove SESSION_ID [--yes]")
	}
	id, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || id <= 0 {
		return errors.New("a positive session ID is required")
	}
	flags := flag.NewFlagSet("pact workspaces remove", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	yes := flags.Bool("yes", false, "Permanently delete the managed workspace, including untracked files, local commits, and build output")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := state.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	session, err := store.Session(ctx, id)
	if err != nil {
		return err
	}
	if !session.WorkspaceManaged {
		return errors.New("operator-supplied workspaces cannot be removed by Pact")
	}
	fmt.Fprintf(output, "Session %d workspace: %s\n", id, session.Workspace)
	sessions, err := store.Sessions(ctx, session.Workspace)
	if err != nil {
		return err
	}
	var ids []string
	for _, reference := range sessions {
		ids = append(ids, strconv.FormatInt(reference.ID, 10))
	}
	fmt.Fprintln(output, "Affected sessions:", strings.Join(ids, ", "))
	if !*yes {
		fmt.Fprintln(output, "Removal permanently deletes all workspace files, including untracked files, local commits, and build output.")
		fmt.Fprintln(output, "All sessions using this directory will lose workspace access. Catalogue, conversation state, and shared Nix storage are retained.")
		fmt.Fprintf(output, "To request removal after ownership checks: pact workspaces remove %d --yes\n", id)
		return nil
	}
	if err := store.RemoveWorkspace(ctx, session.Storage.WorkspaceID); err != nil {
		return err
	}
	fmt.Fprintln(output, "Workspace removed; retained session history is still available.")
	return nil
}
