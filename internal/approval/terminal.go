package approval

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Serve prompts for one decision at a time. It owns input and closes it on exit
// or cancellation to interrupt a pending read. The socket's parent must exist.
func Serve(ctx context.Context, socket string, input *os.File, output io.Writer) error {
	defer input.Close()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen for approvals: %w", err)
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return fmt.Errorf("restrict approval socket access: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	turn := make(chan struct{}, 1)
	turn <- struct{}{}
	inputDone := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/approvals", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		var request Request
		if err != nil || json.Unmarshal(data, &request) != nil || request.ID == "" || request.ExpiresAt.IsZero() {
			http.Error(w, "invalid approval request (maximum 2 MiB)", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), request.ExpiresAt)
		defer cancel()
		select {
		case <-ctx.Done():
			http.Error(w, "request cancelled or expired", http.StatusRequestTimeout)
			return
		case <-turn:
			defer func() { turn <- struct{}{} }()
		}
		if ctx.Err() != nil || !time.Now().Before(request.ExpiresAt) {
			http.Error(w, "request cancelled or expired", http.StatusRequestTimeout)
			return
		}
		if err := input.SetReadDeadline(request.ExpiresAt); err != nil {
			http.Error(w, "approval input does not support read deadlines", http.StatusServiceUnavailable)
			return
		}
		readStopped := make(chan struct{})
		stopRead := context.AfterFunc(ctx, func() {
			_ = input.SetReadDeadline(time.Now())
			close(readStopped)
		})
		defer func() {
			// Finish the interrupt before another prompt takes over the input.
			if !stopRead() {
				<-readStopped
			}
			_ = input.SetReadDeadline(time.Time{})
		}()
		// A cancelled scanner retains its error and partial line; each prompt
		// needs a fresh scanner so neither carries over to another request.
		scanner := bufio.NewScanner(input)
		data, err = json.Marshal(request, jsontext.WithIndent("  "))
		if err != nil {
			http.Error(w, "cannot display approval request", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(output, "\n%s\n", data)
		for {
			fmt.Fprintln(output, "Allow? [y/n]")
			scanned := scanner.Scan()
			if ctx.Err() != nil || !time.Now().Before(request.ExpiresAt) {
				fmt.Fprintln(output, "Request cancelled or expired.")
				http.Error(w, "request cancelled or expired", http.StatusRequestTimeout)
				return
			}
			if !scanned {
				select {
				case inputDone <- scanner.Err():
				default:
				}
				return
			}
			decision := ""
			switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
			case "y":
				decision = "approve"
			case "n":
				decision = "reject"
			default:
				continue
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.MarshalWrite(w, Response{ID: request.ID, Decision: decision})
			return
		}
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		cancel()
		_ = server.Close()
		_ = input.Close()
		// Wait for the active prompt to release the scanner and output.
		<-turn
	}()
	fmt.Fprintf(output, "Listening for approvals on %s\n", socket)
	select {
	case <-ctx.Done():
		return nil
	case err := <-inputDone:
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
