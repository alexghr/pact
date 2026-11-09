// Package pi adapts Pact runs to Pi's native CLI and extensions.
package pi

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/alexghr/pact/internal/harness"
)

//go:embed extension.ts
var extension []byte

// Run keeps Pi attached to the container terminal; the daemon owns the PTY.
func Run(ctx context.Context, options harness.Options, instructions, bindingURL string) error {
	// Let Pi handle terminal interrupts without also terminating its Go parent.
	// Notify, rather than Ignore, keeps the child's SIGINT disposition intact.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)

	if options.BackendRef != "" {
		info, err := os.Stat(options.BackendRef)
		if err != nil {
			return fmt.Errorf("open saved Pi session: %w", err)
		}
		// Pi creates a new conversation for a missing or empty --session file.
		// An explicit Pact resume must never silently select a new conversation.
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("saved Pi session %q is not a nonempty file", options.BackendRef)
		}
	}
	dir, err := os.MkdirTemp("", "pact-pi-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path, err := writeExtension(dir, options, bindingURL)
	if err != nil {
		return err
	}
	args := []string{"--extension", path, "--skill", "/etc/pi/skills", "--append-system-prompt", instructions}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Effort != "" {
		args = append(args, "--thinking", options.Effort)
	}
	if options.BackendRef != "" {
		args = append(args, "--session", options.BackendRef)
	}
	command := exec.CommandContext(ctx, "pi", args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 3 * time.Second
	return command.Run()
}

func writeExtension(dir string, options harness.Options, bindingURL string) (string, error) {
	data, err := json.Marshal(struct {
		harness.Options
		BindingURL string `json:"bindingURL"`
	}{options, bindingURL})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "options.json"), data, 0600); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pact.ts")
	return path, os.WriteFile(path, extension, 0600)
}
