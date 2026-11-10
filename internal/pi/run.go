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
func Run(ctx context.Context, options harness.Options, instructions, bindingURL, authURL string) error {
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
	auth, err := os.ReadFile(authSeed)
	if err != nil {
		return fmt.Errorf("read Pi login snapshot: %w", err)
	}
	authPath := filepath.Join(stateDir, "auth.json")
	if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(authPath, auth, 0600); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "pact-pi-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path, err := writeExtension(dir, options, bindingURL, authURL)
	if err != nil {
		return err
	}
	bundled, err := os.ReadFile("/etc/pi/extensions.json")
	if err != nil {
		return fmt.Errorf("read bundled Pi extensions: %w", err)
	}
	var extensions []string
	if err := json.Unmarshal(bundled, &extensions); err != nil {
		return fmt.Errorf("decode bundled Pi extensions: %w", err)
	}

	args := []string{"--extension", path, "--skill", "/etc/pi/skills", "--append-system-prompt", instructions}
	for _, extension := range extensions {
		args = append(args, "--extension", extension)
	}
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

func writeExtension(dir string, options harness.Options, bindingURL, authURL string) (string, error) {
	data, err := json.Marshal(struct {
		harness.Options
		BindingURL string `json:"bindingURL"`
		AuthURL    string `json:"authURL"`
	}{options, bindingURL, authURL})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "options.json"), data, 0600); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "pact.ts")
	return path, os.WriteFile(path, extension, 0600)
}
