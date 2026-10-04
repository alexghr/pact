package dind

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStartCancellation(t *testing.T) {
	for _, stage := range []string{"run", "info", "retry", "chmod"} {
		t.Run(stage, func(t *testing.T) {
			marker, resources := dockerShim(t, stage)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				defer close(done)
				_, cleanup, err := Start(ctx, resources)
				if cleanup != nil {
					cleanup()
				}
				result <- err
			}()
			stopShimOnFailure(t, marker, done, stage != "retry")
			waitForCommand(t, marker)
			cause := errors.New("launcher interrupted")
			cancel(cause)
			select {
			case err := <-result:
				if !errors.Is(err, cause) {
					t.Fatalf("startup lost its cancellation cause: %v", err)
				}
			case <-time.After(750 * time.Millisecond):
				t.Fatal("cancellation did not stop DinD startup promptly")
			}
			for _, resource := range []string{"rm", "volume"} {
				if _, err := os.Stat(filepath.Join(resources, resource)); err != nil {
					t.Fatalf("cancelled startup did not clean up %s: %v", resource, err)
				}
			}
		})
	}
}

func TestCleanupTimeout(t *testing.T) {
	marker, workspace := dockerShim(t, "rm")
	_, cleanup, err := Start(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cleanup()
	}()
	stopShimOnFailure(t, marker, done, true)
	waitForCommand(t, marker)
	select {
	case <-done:
	case <-time.After(12 * time.Second):
		t.Fatal("stalled Docker cleanup prevented shutdown")
	}
}

func dockerShim(t *testing.T, stage string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "blocked")
	script := `#!/usr/bin/env bash
set -eu
stage="$1"
if [[ "$stage" == exec ]]; then
  stage="$3"
  if [[ "$stage" == docker ]]; then stage=info; fi
fi
if [[ "$stage" == info && "$PACT_DIND_BLOCK" == retry ]]; then
  printf '%s\n' "$$" > "$PACT_DIND_MARKER"
  exit 1
fi
if [[ "$stage" == "$PACT_DIND_BLOCK" ]]; then
  printf '%s\n' "$$" > "$PACT_DIND_MARKER"
  exec sleep 30
fi
case "$stage" in
  rm|volume) touch "$PACT_DIND_RESOURCES/$stage" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PACT_DIND_BLOCK", stage)
	t.Setenv("PACT_DIND_MARKER", marker)
	t.Setenv("PACT_DIND_RESOURCES", dir)
	return marker, dir
}

func waitForCommand(t *testing.T, marker string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(marker); err == nil && len(data) != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Docker shim did not reach the requested startup or cleanup stage")
}

func stopShimOnFailure(t *testing.T, marker string, done <-chan struct{}, blocked bool) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		if blocked {
			data, _ := os.ReadFile(marker)
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			}
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("DinD operation did not finish after stopping the shim")
		}
	})
}
