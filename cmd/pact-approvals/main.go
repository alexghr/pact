package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alexghr/pact/internal/approval"
	"github.com/alexghr/pact/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()

	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	var socket string

	flags := flag.NewFlagSet("pact-approvals", flag.ContinueOnError)
	flags.StringVar(&socket, "socket", "", "Unix socket to listen on (default: approvals.socket in the default Pact config)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}

	if socket == "" {
		path, err := config.DefaultHostPath()
		if err != nil {
			return err
		}
		if err := config.InitEmptyConfig(path); err != nil {
			return err
		}
		cfg, err := config.Load(path)
		if err != nil {
			return err
		}
		socket = cfg.Approvals.Socket
	}

	if socket == "" {
		return errors.New("set approvals.socket in the default Pact config or pass --socket PATH")
	}

	socket, err := filepath.Abs(socket)
	if err != nil {
		return err
	}

	return approval.ServeMacOS(ctx, socket, os.Stdout)
}
