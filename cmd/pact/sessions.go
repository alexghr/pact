package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/session"
)

type listSessionsConfig struct {
	workspace string
	json      bool
}

type showSessionConfig struct {
	sessionID int64
	json      bool
}

func manageSessions(ctx context.Context, args []string, output, errOut io.Writer, hostCfg config.HostConfig) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return listSessions(ctx, args[1:], output, errOut, hostCfg)
		case "show":
			return showSession(ctx, args[1:], output, errOut, hostCfg)
		default:
			return fmt.Errorf("unknown sessions command %q", args[0])
		}
	}

	return errors.New("usage: pact sessions list [--workspace PATH | --all] [--json] | show ID [--json]")
}

func listSessions(ctx context.Context, args []string, output, errOut io.Writer, hostCfg config.HostConfig) (runErr error) {
	cfg, err := parseListSessionsArgs(args, errOut)
	if err != nil {
		return err
	}

	store, err := session.OpenStore(ctx, hostCfg.DatabasePath)
	if err != nil {
		return err
	}

	defer func() { runErr = errors.Join(runErr, store.Close()) }()

	sessions, err := store.Sessions(ctx, cfg.workspace)
	if err != nil {
		return err
	}

	if cfg.json {
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

func showSession(ctx context.Context, args []string, output, errOut io.Writer, hostCfg config.HostConfig) (runErr error) {
	cfg, err := parseShowSessionArgs(args, errOut)
	if err != nil {
		return err
	}

	store, err := session.OpenStore(ctx, hostCfg.DatabasePath)
	if err != nil {
		return err
	}

	defer func() { runErr = errors.Join(runErr, store.Close()) }()

	session, err := store.Session(ctx, cfg.sessionID)
	if err != nil {
		return err
	}

	if cfg.json {
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

func parseListSessionsArgs(args []string, output io.Writer) (listSessionsConfig, error) {
	var cfg listSessionsConfig

	flags := flag.NewFlagSet("pact sessions list", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.json, "json", false, "Print machine-readable JSON")
	flags.StringVar(&cfg.workspace, "workspace", "", "Filter by host workspace (default: all workspaces)")

	if err := flags.Parse(args); err != nil {
		return cfg, err
	}

	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	if cfg.workspace != "" {
		var err error
		cfg.workspace, err = filepath.Abs(cfg.workspace)
		if err != nil {
			return cfg, err
		}

		cfg.workspace, err = filepath.EvalSymlinks(cfg.workspace)
		if err != nil {
			return cfg, fmt.Errorf("resolve workspace: %w", err)
		}
	}

	return cfg, nil
}

func parseShowSessionArgs(args []string, output io.Writer) (showSessionConfig, error) {
	var cfg showSessionConfig

	if len(args) == 0 {
		return cfg, errors.New("a session ID is required")
	}

	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return cfg, errors.New("a session ID is required")
	}
	cfg.sessionID = id

	flags := flag.NewFlagSet("pact sessions show", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&cfg.json, "json", false, "Print machine-readable JSON")

	if err := flags.Parse(args[1:]); err != nil {
		return cfg, err
	}

	if flags.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	return cfg, nil
}
