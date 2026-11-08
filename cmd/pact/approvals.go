package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexghr/pact/internal/approval"
	"github.com/alexghr/pact/internal/config"
)

func serveApprovals(ctx context.Context, args []string, hostCfg config.HostConfig) error {
	flags := flag.NewFlagSet("pact approvals", flag.ContinueOnError)
	var socket string
	flags.StringVar(&socket, "socket", "", "Unix socket to listen on (default: approvals.socket in the Pact config)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	if socket == "" {
		socket = hostCfg.Approvals.Socket
	}
	if socket == "" {
		return errors.New("set approvals.socket in the Pact config or pass --socket PATH")
	}
	socket, err := filepath.Abs(socket)
	if err != nil {
		return err
	}
	// Reopen stdin so closing it can interrupt a terminal read on shutdown.
	input, err := os.Open("/dev/stdin")
	if err != nil {
		return err
	}
	return approval.Serve(ctx, socket, input, os.Stdout)
}
