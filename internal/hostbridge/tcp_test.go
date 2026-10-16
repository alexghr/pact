package hostbridge

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/environment"
	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

func TestTCPAuthenticationAndLifetime(t *testing.T) {
	var calls atomic.Int32
	bridge, err := startTCP([]Server{{Name: "example", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/mcp/example" {
			t.Error("bridge token reached handler or request path changed")
		}
		w.WriteHeader(http.StatusNoContent)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	base, err := url.Parse(bridge.hostURL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := bridge.hostURL + "/mcp/example"
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	// Prefix lookalikes must not grant access or trigger token-bearing redirects.
	for _, path := range []string{"", "/wrong", base.Path + "extra", base.Path} {
		req, err := http.NewRequest(http.MethodPost, base.Scheme+"://"+base.Host+path+"/mcp/example", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := http.StatusUnauthorized
		if path == base.Path {
			want = http.StatusNoContent
		}
		if resp.StatusCode != want {
			t.Fatalf("got %d, want %d", resp.StatusCode, want)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times", calls.Load())
	}
	// Opt in when a local Docker Desktop daemon and the Pact image are available.
	if os.Getenv("PACT_TEST_DOCKER") == "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
		defer cancel()
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		store, err := state.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		launch, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: t.TempDir(), Owner: "test", Backend: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		env := launch.Environment
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			_ = exec.CommandContext(cleanupCtx, "docker", "rm", "--force", env.NixVolume).Run()
			_ = exec.CommandContext(cleanupCtx, "docker", "volume", "rm", env.StateVolume, env.NixVolume).Run()
		})
		image, err := container.BuildAgentImage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		unlock, err := store.LockStorage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = environment.Prepare(ctx, store, env)
		unlock()
		if err != nil {
			t.Fatal(err)
		}
		args := append([]string{"run", "--rm"}, environment.Mounts(env)...)
		args = append(args, "--entrypoint", "/opt/pact/tools/bin/curl", image,
			"--noproxy", "*", "--fail", "--silent", "--show-error", "--max-time", "10", bridge.AgentURL()+"/mcp/example")
		cmd := exec.CommandContext(ctx, "docker", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Docker Desktop bridge: %v: %s", err, strings.ReplaceAll(string(output), base.Path, "/<token>"))
		}
	}
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(endpoint); err == nil {
		t.Fatal("bridge accepted requests after shutdown")
	}
}
