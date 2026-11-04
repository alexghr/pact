package approval

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// serve serializes decisions, so prompts and any in-memory grants have one owner.
func serve(ctx context.Context, socket string, output io.Writer, decide func(context.Context, Request) (bool, error)) error {
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
		approved, err := decide(ctx, request)
		if ctx.Err() != nil || !time.Now().Before(request.ExpiresAt) {
			http.Error(w, "request cancelled or expired", http.StatusRequestTimeout)
			return
		}
		if err != nil {
			fmt.Fprintf(output, "Approval failed: %v\n", err)
			http.Error(w, "approval service unavailable", http.StatusServiceUnavailable)
			return
		}
		decision := "reject"
		if approved {
			decision = "approve"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.MarshalWrite(w, Response{ID: request.ID, Decision: decision})
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
		// Wait for the active prompt to release its input or child process.
		<-turn
	}()
	fmt.Fprintf(output, "Listening for approvals on %s\n", socket)
	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
