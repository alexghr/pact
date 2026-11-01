package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/docker"
	"github.com/alexghr/pact/internal/images"
	"github.com/alexghr/pact/internal/state"
)

func TestSharedStorage(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for disposable real Docker/Nix storage checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := OpenStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	// Use isolated real resources; never remove the operator's shared daemon.
	nix := Nix{Container: "pact-test-nix-" + strings.ToLower(rand.Text()), Volume: "pact-test-store-" + strings.ToLower(rand.Text())}
	image, err := images.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var environments []state.Storage
	for range 2 {
		launch, err := createRecord(ctx, store, Options{Workspace: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		environments = append(environments, launch.Storage)
	}
	shared := nix.Container
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, _ = dockerOutput(cleanupCtx, "rm", "--force", shared)
		for _, env := range environments {
			_, _ = dockerOutput(cleanupCtx, "volume", "rm", env.StateVolume)
		}
		_, _ = dockerOutput(cleanupCtx, "volume", "rm", nix.Volume)
	})
	nix.Image, err = images.GetNixImageName(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(env state.Storage) error {
		if err := nix.prepare(ctx); err != nil {
			return err
		}
		return prepareStorage(ctx, store, env)
	}
	for _, env := range environments {
		if err := prepare(env); err != nil {
			t.Fatal(err)
		}
	}
	client := func(env state.Storage, script string) (string, error) {
		config := docker.ContainerConfig{Image: image, Remove: true,
			Mounts: []docker.Mount{{Type: "volume", Source: env.StateVolume, Target: "/home/pact/.codex"}},
			Env: []string{"HOST_UID=" + strconv.Itoa(os.Getuid()), "HOST_GID=" + strconv.Itoa(os.Getgid()),
				"PACT_AGENT_STATE_DIR=/home/pact/.codex", "PACT_TEST_STATE=" + env.Key},
			Command: []string{"bash", "-euc", script},
		}
		configureStorage(nix, &config)
		var output strings.Builder
		err := docker.RunContainer(ctx, config, docker.Streams{Stdout: &output, Stderr: &output})
		return strings.TrimSpace(output.String()), err
	}
	build := `
if test -e /home/pact/.codex/history; then
  test "$(cat /home/pact/.codex/history)" = "$PACT_TEST_STATE"
fi
printf '%s' "$PACT_TEST_STATE" > /home/pact/.codex/history
test -r /etc/codex/skills/graphene-stacked-prs/SKILL.md
codex --version >&2
if touch /nix/store/forbidden 2>/dev/null; then exit 1; fi
if touch /nix/var/nix/gcroots/forbidden 2>/dev/null; then exit 1; fi
export PACT_TEST_TOOLS="$(dirname "$(dirname "$(command -v bash)")")"
mkdir -p /tmp/project
cd /tmp/project
mkdir src
printf shared > src/message
cat > default.nix <<'NIX'
let tools = builtins.storePath (builtins.getEnv "PACT_TEST_TOOLS"); in
builtins.derivation {
  name = "pact-source-check";
  system = builtins.currentSystem;
  src = ./src;
  builder = "${tools}/bin/bash";
  args = [ "-ec" ''
    if ${tools}/bin/touch /nix/var/nix/gcroots/forbidden 2>/dev/null; then exit 1; fi
    ${tools}/bin/mkdir -p "$out/bin"
    printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$( ${tools}/bin/cat "$src/message" )" > "$out/bin/hello"
    ${tools}/bin/chmod +x "$out/bin/hello"
  '' ];
}
NIX
nix shell --impure --file default.nix --command hello
nix build --impure --file default.nix --no-link --print-out-paths
`
	type result struct {
		output string
		err    error
	}
	clients := make(chan result, 2)
	for _, env := range environments {
		go func() { output, err := client(env, build); clients <- result{output, err} }()
	}
	first, second := <-clients, <-clients
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent builds: %v: %s\n%v: %s", first.err, first.output, second.err, second.output)
	}
	path := func(output string) string {
		for line := range strings.Lines(output) {
			if strings.HasPrefix(strings.TrimSpace(line), "/nix/store/") {
				return strings.TrimSpace(line)
			}
		}
		t.Fatalf("no output store path: %s", output)
		return ""
	}
	if path(first.output) != path(second.output) || !strings.Contains(first.output, "shared\n") {
		t.Fatalf("clients did not reuse and execute one package: %s\n%s", first.output, second.output)
	}
	if _, err := dockerOutput(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	for _, env := range environments {
		if err := prepare(env); err != nil {
			t.Fatal(err)
		}
		output, err := client(env, build)
		if err != nil || path(output) != path(first.output) {
			t.Fatalf("state or cached build lost after container replacement: %v: %s", err, output)
		}
	}
	// A service revision mismatch must leave the existing daemon untouched.
	oldImage := shared + "-old-daemon"
	if _, err := dockerOutput(ctx, "commit", "--change", "ENV PACT_TEST_REVISION=old", shared, oldImage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = dockerOutput(cleanupCtx, "rm", "--force", shared)
		_, _ = dockerOutput(cleanupCtx, "image", "rm", oldImage)
	})
	if _, err := dockerOutput(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	oldID, err := dockerOutput(ctx, "run", "--detach", "--name", shared, "--mount", "type=volume,src="+nix.Volume+",dst=/nix", oldImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepare(environments[0]); err == nil || !strings.Contains(err.Error(), "incompatible Nix daemon") {
		t.Fatalf("daemon mismatch was accepted: %v", err)
	}
	if id, err := dockerOutput(ctx, "inspect", "--format", "{{.Id}}", shared); err != nil || id != oldID {
		t.Fatalf("incompatible daemon was replaced: %s, %v", id, err)
	}
	if _, err := dockerOutput(ctx, "exec", shared, "nix", "--store", "daemon", "store", "info"); err != nil {
		t.Fatalf("incompatible daemon was interrupted: %v", err)
	}
	// Simulate an intentional Docker wipe: the catalogue and private state remain.
	if _, err := dockerOutput(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	if _, err := dockerOutput(ctx, "volume", "rm", nix.Volume); err != nil {
		t.Fatal(err)
	}
	// Retained sessions can use a replacement service without catalogue edits.
	nix.Container += "-replacement"
	nix.Volume += "-replacement"
	shared = nix.Container
	for _, env := range environments {
		if err := prepare(env); err != nil {
			t.Fatalf("missing shared cache was not recreated: %v", err)
		}
		if _, err := client(env, `test "$(cat /home/pact/.codex/history)" = "$PACT_TEST_STATE"; codex --version`); err != nil {
			t.Fatalf("cache recovery lost private state or tools: %v", err)
		}
		if _, err := client(env, build); err != nil {
			t.Fatalf("project dependency did not rebuild: %v", err)
		}
	}
	// Established private state must never be replaced with an empty volume.
	if _, err := dockerOutput(ctx, "volume", "rm", environments[0].StateVolume); err != nil {
		t.Fatal(err)
	}
	if err := prepare(environments[0]); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("missing established state was silently recreated: %v", err)
	}
}

// Fixture operations use the real CLI independently of Pact's resource policy.
func dockerOutput(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
