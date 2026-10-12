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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// The image stores its Nix tools in a tarball until its first startup.
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--entrypoint", "/bin/bash", "pact-codex", "-e", "-c",
			`mkdir -p /nix/store; tar -xf /opt/pact/store.tar -C /nix/store; exec /opt/pact/tools/bin/curl "$@"`,
			"bridge-check", "--noproxy", "*", "--fail", "--silent", "--show-error", "--max-time", "10", bridge.AgentURL()+"/mcp/example")
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
