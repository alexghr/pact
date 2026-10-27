package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

func runInteractive(ctx context.Context, serverConfig, native []string, backendRef string, bind func(context.Context, string) error) error {
	// Catch Ctrl-C without inheriting an ignored disposition into the TUI.
	// Its app-server lives in a separate process group and must stay running.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	dir, err := os.MkdirTemp("", "pact-agent-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "app-server.sock")
	serverArgs := append(slices.Clone(serverConfig), "app-server", "--listen", "unix://"+socket)
	server := exec.Command("codex", serverArgs...)
	server.Stdout, server.Stderr = os.Stderr, os.Stderr
	// The server must not consume terminal input or receive the TUI's Ctrl-C.
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	serverDone := make(chan struct{})
	var serverErr error
	go func() {
		serverErr = server.Wait()
		close(serverDone)
	}()
	defer stopServer(server, serverDone)
	client, err := connect(ctx, socket, serverDone)
	if err != nil {
		return err
	}
	defer client.Close()

	if backendRef != "" {
		resumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := client.PrepareResume(resumeCtx, backendRef)
		cancel()
		if err != nil {
			return fmt.Errorf("prepare resumed thread: %w", err)
		}
	}

	tuiCtx, stopTUI := context.WithCancel(ctx)
	defer stopTUI()
	// TUI arguments are separate from server configuration: new threads need
	// launch settings, but remote resume rejoins the thread prepared above.
	tuiArgs := []string{"--remote", "unix://" + socket}
	tuiArgs = append(tuiArgs, native...)
	tui := exec.CommandContext(tuiCtx, "codex", tuiArgs...)
	tui.Stdin, tui.Stdout, tui.Stderr = os.Stdin, os.Stdout, os.Stderr
	tui.Cancel = func() error { return tui.Process.Signal(syscall.SIGTERM) }
	tui.WaitDelay = 3 * time.Second
	if err := tui.Start(); err != nil {
		return fmt.Errorf("start Codex TUI: %w", err)
	}
	tuiDone := make(chan error, 1)
	go func() { tuiDone <- tui.Wait() }()
	reaped := false
	// Always reap the TUI before closing its server, including observer failures.
	defer func() {
		stopTUI()
		if !reaped {
			<-tuiDone
		}
	}()
	for {
		select {
		case err := <-tuiDone:
			reaped = true
			select {
			case ref := <-client.Bindings():
				if err := bind(context.Background(), ref); err != nil {
					return err
				}
			default:
			}
			return err
		case ref := <-client.Bindings():
			if err := bind(ctx, ref); err != nil {
				return err
			}
		case <-client.Done():
			return fmt.Errorf("Codex observer disconnected: %w", client.Err())
		case <-serverDone:
			if serverErr == nil {
				serverErr = fmt.Errorf("unexpected exit")
			}
			return fmt.Errorf("Codex app-server stopped: %w", serverErr)
		}
	}
}

func connect(ctx context.Context, socket string, serverDone <-chan struct{}) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := Dial(ctx, socket)
		if err == nil {
			return client, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect Codex app-server: %w", errors.Join(err, ctx.Err()))
		case <-serverDone:
			return nil, fmt.Errorf("Codex app-server exited before accepting connections: %w", err)
		case <-ticker.C:
		}
	}
}

func stopServer(cmd *exec.Cmd, done <-chan struct{}) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
