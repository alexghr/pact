package dind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartCancellation(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	// Stall sidecar creation to reproduce the interrupted-startup regression.
	script := `#!/usr/bin/env bash
if [[ "$1" == run ]]; then
  touch "$PACT_DIND_STARTED"
  exec sleep 5
fi
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PACT_DIND_STARTED", marker)
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("launcher interrupted")
	interrupted := make(chan struct{})
	go func() {
		defer close(interrupted)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err == nil {
					cancel(cause)
					return
				}
			}
		}
	}()
	started := time.Now()
	_, cleanup, err := Start(ctx, dir)
	elapsed := time.Since(started)
	cancel(nil)
	<-interrupted
	if cleanup != nil {
		cleanup()
	}
	if !errors.Is(err, cause) {
		t.Fatalf("startup lost its cancellation cause: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancelled startup waited for the stalled subprocess: %s", elapsed)
	}
}
