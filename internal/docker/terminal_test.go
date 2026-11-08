package docker_test

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/images"
)

func TestTerminalPipeInputAndResize(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real Docker checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	image, err := images.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := docker.CreateContainer(ctx, docker.ContainerConfig{
		Image: image, TTY: true, Interactive: true, Network: "none", Entrypoint: "/bin/bash",
		Command: []string{"-c", `read -r value; stty size; printf 'received:%s\n' "$value"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = docker.RemoveContainer(cleanup, id, true, true)
	}()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	output, sink := io.Pipe()
	defer output.Close()
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := docker.AttachTerminal(ctx, id, docker.Streams{Stdin: input, Stdout: sink}, docker.TerminalSize{Rows: 37, Cols: 91}, func() error {
			close(ready)
			return nil
		})
		_ = sink.CloseWithError(err)
		done <- err
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("terminal startup: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := writer.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	var received strings.Builder
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		received.WriteString(scanner.Text())
		received.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if text := received.String(); !strings.Contains(text, "37 91") || !strings.Contains(text, "received:hello") {
		t.Fatalf("terminal lost input or dimensions: %q", text)
	}
}
