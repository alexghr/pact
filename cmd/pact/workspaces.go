package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/alexghr/pact/internal/session"
)

type removeWorkspaceConfig struct {
	sessionID int64
	yes       bool
}

func manageWorkspaces(ctx context.Context, args []string, output, errOut io.Writer) (runErr error) {
	if len(args) > 0 {
		switch args[0] {
		case "remove":
			return removeWorkspace(ctx, args[1:], output, errOut)
		default:
			return fmt.Errorf("unknown workspaces command %q", args[0])
		}
	}

	return errors.New("usage: pact workspaces remove SESSION_ID [--yes]")
}

func removeWorkspace(ctx context.Context, args []string, output, errOut io.Writer) (runErr error) {
	cfg, err := parseRemoveWorkspaceArgs(args, errOut)
	if err != nil {
		return err
	}

	store, err := session.OpenStore(ctx)
	if err != nil {
		return err
	}

	defer func() { runErr = errors.Join(runErr, store.Close()) }()

	record, err := store.Session(ctx, cfg.sessionID)
	if err != nil {
		return err
	}

	if !record.WorkspaceManaged {
		return errors.New("operator-supplied workspaces cannot be removed by Pact")
	}

	fmt.Fprintf(output, "Session %d workspace: %s\n", cfg.sessionID, record.Workspace)
	sessions, err := store.Sessions(ctx, record.Workspace)
	if err != nil {
		return err
	}

	var ids []string
	for _, reference := range sessions {
		ids = append(ids, strconv.FormatInt(reference.ID, 10))
	}

	fmt.Fprintln(output, "Affected sessions:", strings.Join(ids, ", "))

	if !cfg.yes {
		fmt.Fprintln(output, "Removal permanently deletes all workspace files, including untracked files, local commits, and build output.")
		fmt.Fprintln(output, "All sessions using this directory will lose workspace access. Catalogue, conversation state, and shared Nix storage are retained.")
		fmt.Fprintf(output, "To request removal after ownership checks: pact workspaces remove %d --yes\n", cfg.sessionID)
		return nil
	}

	if err := session.RemoveWorkspace(ctx, store, record.WorkspaceID); err != nil {
		return err
	}

	fmt.Fprintln(output, "Workspace removed; retained session history is still available.")
	return nil
}

func parseRemoveWorkspaceArgs(args []string, output io.Writer) (removeWorkspaceConfig, error) {
	var cfg removeWorkspaceConfig

	if len(args) == 0 {
		return cfg, errors.New("a session ID is required")
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return cfg, errors.New("a session ID is required")
	}
	cfg.sessionID = id

	flags := flag.NewFlagSet("pact workspaces remove", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.yes, "yes", false, "Permanently delete the managed workspace, including untracked files, local commits, and build output")

	if err := flags.Parse(args[1:]); err != nil {
		return cfg, err
	}

	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	return cfg, nil
}
