package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/config"
	"github.com/alexghr/pact/internal/docker"
)

func TestDaemonSocketLifecycle(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	socket, err := socketPath(true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, config.HostConfig{DatabasePath: filepath.Join(t.TempDir(), "sessions.db")}) }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var conn net.Conn
	for conn == nil {
		conn, _ = net.Dial("unix", socket)
		if conn != nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("daemon exited before listening: %v", err)
		case <-ctx.Done():
			t.Fatal("daemon did not start listening")
		case <-ticker.C:
		}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err := json.NewEncoder(conn).Encode(request{SessionID: 999}); err != nil {
		t.Fatal(err)
	}
	var reply response
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Error, "not owned by this daemon") {
		t.Fatalf("unknown session response: %+v", reply)
	}
	// A client that has not sent its command must not prevent daemon shutdown.
	pending, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer pending.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("daemon did not release its clients")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("daemon socket remains after shutdown: %v", err)
	}
}

// Exercise the actual attachment transport with a real process: disconnecting
// a terminal must leave its input open and retain output for the next client.
func TestDetachKeepsProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	current := newLiveSession()
	input, writer := io.Pipe()
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		current.copyInput(ctx, writer)
	}()
	cmd := exec.CommandContext(ctx, "cat")
	cmd.Stdin, cmd.Stdout = input, current
	defer func() {
		_ = writer.Close()
		_ = input.Close()
		cancel()
		<-inputDone
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	socket := filepath.Join(t.TempDir(), "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	attach := func() (net.Conn, *json.Decoder, <-chan error) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				done <- err
				return
			}
			defer conn.Close()
			done <- current.attach(ctx, json.NewDecoder(conn), json.NewEncoder(conn), docker.TerminalSize{Rows: 24, Cols: 80})
		}()
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		return conn, json.NewDecoder(conn), done
	}
	readUntil := func(decoder *json.Decoder, target string) string {
		t.Helper()
		var output strings.Builder
		for !strings.Contains(output.String(), target) {
			var message response
			if err := decoder.Decode(&message); err != nil {
				t.Fatal(err)
			}
			output.Write(message.Data)
		}
		return output.String()
	}
	// Queue input before the process reads anything. Socket EOF must still
	// release this attachment without closing the session's input pipe.
	pending, pendingDecoder, disconnected := attach()
	var history response
	if err := pendingDecoder.Decode(&history); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(pending).Encode(request{Data: []byte("before startup\n")}); err != nil {
		t.Fatal(err)
	}
	_ = pending.Close()
	select {
	case err := <-disconnected:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked input prevented attachment release")
	}
	first, decoder, done := attach()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readUntil(decoder, "before startup\n")
	if err := json.NewEncoder(first).Encode(request{Data: []byte("first\n")}); err != nil {
		t.Fatal(err)
	}
	readUntil(decoder, "first\n")
	_ = first.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("while detached\n")); err != nil {
		t.Fatal(err)
	}
	second, decoder, done := attach()
	if err := json.NewEncoder(second).Encode(request{Data: []byte("second\n")}); err != nil {
		t.Fatal(err)
	}
	output := readUntil(decoder, "second\n")
	if !strings.Contains(output, "first\n") || !strings.Contains(output, "while detached\n") {
		t.Fatalf("reattachment lost output: %q", output)
	}
	if _, err := writer.Write([]byte("last output\n")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	close(current.done)
	var final strings.Builder
	for {
		var message response
		if err := decoder.Decode(&message); err != nil {
			t.Fatal(err)
		}
		final.Write(message.Data)
		if message.Done {
			break
		}
	}
	if !strings.Contains(final.String(), "last output\n") {
		t.Fatalf("process exit lost its final output: %q", final.String())
	}
	_ = second.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsafeSocketDirectory(t *testing.T) {
	for _, name := range []string{"public", "symlink"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			socket, err := socketPath(true)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Dir(socket)
			switch name {
			case "public":
				err = os.Chmod(dir, 0777)
			case "symlink":
				err = os.Rename(dir, dir+"-real")
				if err == nil {
					err = os.Symlink(dir+"-real", dir)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, create := range []bool{false, true} {
				if _, err := socketPath(create); err == nil {
					t.Fatalf("unsafe socket directory accepted (create=%t)", create)
				}
			}
		})
	}
}
