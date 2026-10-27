// pact-agent dispatches Pact launch requests inside the agent container.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/harness"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pact-agent:", err)
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() > 0 {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "codex" {
		return fmt.Errorf("expected backend and JSON launch options")
	}
	var options harness.Options
	if err := json.Unmarshal([]byte(args[1]), &options); err != nil {
		return fmt.Errorf("decode launch options: %w", err)
	}
	instructions, err := os.ReadFile("/opt/pact/agent-instructions.md")
	if err != nil {
		return fmt.Errorf("read agent instructions: %w", err)
	}
	return codex.Run(ctx, options, string(instructions), func(ctx context.Context, ref string) error {
		return harness.PublishBinding(ctx, os.Getenv("PACT_BINDING_URL"), ref)
	})
}
