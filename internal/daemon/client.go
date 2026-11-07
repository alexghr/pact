package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/session"
)

func Start(ctx context.Context, options session.Options, detach bool) error {
	return connect(ctx, request{Options: &options, Detach: detach})
}

func Attach(ctx context.Context, id int64) error {
	return connect(ctx, request{SessionID: id})
}

func connect(ctx context.Context, req request) error {
	socket, err := socketPath(false)
	if err != nil {
		return fmt.Errorf("connect to Pact daemon: %w", err)
	}
	var terminal *os.File
	req.Size = docker.TerminalSize{Rows: 24, Cols: 80}
	if !req.Detach {
		terminal, err = os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer terminal.Close()
		req.Size = terminalSize(terminal)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("connect to Pact daemon (start it with pact daemon): %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	encoder, decoder := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := encoder.Encode(req); err != nil {
		return err
	}
	var initial response
	if err := decoder.Decode(&initial); err != nil {
		return err
	}
	if initial.Error != "" {
		return errors.New(initial.Error)
	}
	fmt.Fprintf(os.Stderr, "Pact session %d\n", initial.SessionID)
	if req.Detach {
		return nil
	}
	fmt.Fprintln(os.Stderr, "Detach with Ctrl-].")
	saved, err := stty(terminal, "-g")
	if err != nil {
		return err
	}
	if _, err := stty(terminal, "raw", "-echo"); err != nil {
		return err
	}
	defer func() {
		restoreTerminal(terminal, saved)
		fmt.Fprintln(os.Stderr)
	}()
	finished := make(chan error, 2)
	outputDone := make(chan struct{})
	defer func() {
		// Stop remote output before restoring local modes: a final TUI update
		// could otherwise turn mouse reporting back on after cleanup.
		_ = conn.Close()
		<-outputDone
	}()
	go func() {
		defer close(outputDone)
		for {
			var message response
			if err := decoder.Decode(&message); err != nil {
				finished <- err
				return
			}
			if len(message.Data) > 0 {
				if _, err := terminal.Write(message.Data); err != nil {
					finished <- err
					return
				}
			}
			if message.Error != "" {
				finished <- errors.New(message.Error)
				return
			}
			if message.Done {
				finished <- nil
				return
			}
		}
	}()
	var writes sync.Mutex
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := terminal.Read(buffer)
			if err != nil {
				finished <- err
				return
			}
			data := buffer[:n]
			// Ctrl-] belongs to Pact; Ctrl-C and all other input reach the TUI.
			if strings.ContainsRune(string(data), '\x1d') {
				finished <- nil
				return
			}
			writes.Lock()
			err = encoder.Encode(request{Data: data})
			writes.Unlock()
			if err != nil {
				finished <- err
				return
			}
		}
	}()
	resized := make(chan os.Signal, 1)
	signal.Notify(resized, syscall.SIGWINCH)
	defer signal.Stop(resized)
	for {
		select {
		case err := <-finished:
			return err
		case <-ctx.Done():
			return nil
		case <-resized:
			writes.Lock()
			err := encoder.Encode(request{Size: terminalSize(terminal)})
			writes.Unlock()
			if err != nil {
				return err
			}
		}
	}
}

func stty(terminal *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin, cmd.Stderr = terminal, os.Stderr
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func terminalSize(terminal *os.File) docker.TerminalSize {
	size := docker.TerminalSize{Rows: 24, Cols: 80}
	if output, err := stty(terminal, "size"); err == nil {
		_, _ = fmt.Sscan(output, &size.Rows, &size.Cols)
	}
	return size
}

func restoreTerminal(terminal *os.File, saved string) {
	// stty restores input processing, but the TUI also changes emulator modes.
	// Release mouse/focus/paste capture and restore the shell's screen/cursor.
	_, _ = terminal.WriteString("\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l" +
		"\x1b[?1004l\x1b[?2004l\x1b[?1049l\x1b[?25h\x1b[0m")
	_, _ = stty(terminal, saved)
}
