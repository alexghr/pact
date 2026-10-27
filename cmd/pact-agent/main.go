// pact-agent reports the initial Codex conversation and runs the native TUI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/alexghr/pact/internal/codex"
	"github.com/alexghr/pact/internal/harness"
)

func main() {
	// Swallow SIGINT here so Ctrl-C cannot tear down the TUI's server. Use Notify,
	// not Ignore: an ignored signal disposition survives exec into the TUI,
	// whereas this handler is reset. The TUI retains its own Ctrl-C behavior.
	// SIGTERM/SIGHUP still stop the supervisor and its children.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
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
	separator := slices.Index(args, "--")
	if len(args) == 0 || args[0] != "run" || separator < 1 {
		return fmt.Errorf("expected run [server configuration] -- [native TUI arguments]")
	}
	serverConfig, native := args[1:separator], args[separator+1:]
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

	httpClient := &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer httpClient.CloseIdleConnections()
	if threadID := os.Getenv("PACT_RESUME_THREAD"); threadID != "" {
		resumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := client.PrepareResume(resumeCtx, threadID)
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
				if err := publish(context.Background(), httpClient, harness.Event{Type: "binding", BackendRef: ref}); err != nil {
					return err
				}
			default:
			}
			return err
		case ref := <-client.Bindings():
			if err := publish(ctx, httpClient, harness.Event{Type: "binding", BackendRef: ref}); err != nil {
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

func connect(ctx context.Context, socket string, serverDone <-chan struct{}) (*codex.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := codex.Dial(ctx, socket)
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

func publish(ctx context.Context, client *http.Client, event harness.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, os.Getenv("PACT_EVENTS_URL"), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("publish Pact thread metadata: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("publish Pact thread metadata: %s", res.Status)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	return err
}
