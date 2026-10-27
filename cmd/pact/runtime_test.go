package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

func TestAuthSnapshotExcludesRenewableAndUnrelatedCredentials(t *testing.T) {
	source := filepath.Join(t.TempDir(), "auth.json")
	login := []byte(`{
		"auth_mode":"chatgpt",
		"OPENAI_API_KEY":"unrelated-api-secret",
		"futureCredential":"unknown-top-level-secret",
		"tokens":{
			"id_token":"identity-token", "access_token":"access-token",
			"refresh_token":"renewable-secret", "account_id":"account",
			"futureCredential":"unknown-nested-secret"
		},
		"last_refresh":"2020-01-01T00:00:00Z"
	}`)
	if err := os.WriteFile(source, login, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := createAuthSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(snapshot) })
	data, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"unrelated-api-secret", "unknown-top-level-secret", "renewable-secret", "unknown-nested-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("authentication snapshot leaked an excluded credential")
		}
	}
	if !bytes.Contains(data, []byte("access-token")) || !bytes.Contains(data, []byte("2020-01-01T00:00:00Z")) {
		t.Fatal("authentication snapshot lost access or masked native staleness checks")
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("snapshot must be private to its owner: %v", err)
	}
	unchanged, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(login, unchanged) {
		t.Fatalf("snapshot creation changed the host login: %v", err)
	}
}

func TestManagedLaunchFailureCanResume(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	loginHome := t.TempDir()
	t.Setenv("HOME", loginHome)
	if err := os.Mkdir(filepath.Join(loginHome, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loginHome, ".codex", "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"id_token":"test","access_token":"test","account_id":"test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	// A real unreachable endpoint fails startup before any agent can run.
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	if err := run([]string{"-p", "Investigate deployment errors"}); err == nil {
		t.Fatal("launch succeeded without Docker")
	}
	store, err := state.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sessions, err := store.Sessions(t.Context(), "")
	if err != nil || len(sessions) != 1 || sessions[0].Status != "startupFailed" || !sessions[0].WorkspaceManaged {
		t.Fatalf("failed managed launch lost its session: %+v, %v", sessions, err)
	}
	session := sessions[0]
	file := filepath.Join(session.Workspace, "unfinished")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"resume", fmt.Sprint(session.ID)}); err == nil {
		t.Fatal("resume succeeded without Docker")
	}
	after, err := store.Session(t.Context(), session.ID)
	if err != nil || after.Environment != session.Environment || after.Workspace != session.Workspace {
		t.Fatalf("resume changed retained environment: %+v, %v", after, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep" {
		t.Fatalf("failed resume changed workspace files: %q, %v", data, err)
	}
}

func TestRuntimeCancellationStopsContainer(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for real Docker checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	label := "me.alexghr.pact.test=" + rand.Text()
	containers := func(ctx context.Context) (string, error) {
		output, err := exec.CommandContext(ctx, "docker", "container", "ls", "--all", "--filter", "label="+label, "--format", "{{.ID}}").CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ids, _ := containers(cleanupCtx)
		if ids != "" {
			_ = exec.CommandContext(cleanupCtx, "docker", append([]string{"rm", "--force"}, strings.Fields(ids)...)...).Run()
		}
	})
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan struct{})
	type result struct {
		started bool
		stopped bool
		err     error
	}
	finished := make(chan result, 1)
	go func() {
		started, stopped, err := runContainer(runCtx, docker.ContainerConfig{
			Image: image, Labels: map[string]string{"me.alexghr.pact.test": strings.TrimPrefix(label, "me.alexghr.pact.test=")},
			Network: "none", Entrypoint: "/bin/bash", Command: []string{"-euc", "printf 'ready\\n'; exec sleep infinity"},
		}, func() error { return nil }, func(input io.Reader) error {
			reader := bufio.NewReader(input)
			line, err := reader.ReadString('\n')
			if err != nil || line != "ready\n" {
				return fmt.Errorf("runtime did not become ready: %q, %v", line, err)
			}
			close(ready)
			stop()
			_, err = io.Copy(io.Discard, reader)
			return err
		})
		finished <- result{started, stopped, err}
	}()
	select {
	case result := <-finished:
		if !result.started || !result.stopped || result.err == nil {
			t.Fatalf("interrupted runtime: started %v, %v", result.started, result.err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled runtime did not release its event stream")
	}
	select {
	case <-ready:
	default:
		t.Fatal("runtime exited before the readiness event")
	}
	if ids, err := containers(ctx); err != nil || ids != "" {
		t.Fatalf("container survived cancellation: %q, %v", ids, err)
	}
}
