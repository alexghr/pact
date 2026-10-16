package environment

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexghr/pact/internal/nix"
	"github.com/alexghr/pact/internal/state"
)

func TestSharedStorage(t *testing.T) {
	if os.Getenv("PACT_TEST_DOCKER") != "1" {
		t.Skip("set PACT_TEST_DOCKER=1 for disposable real Docker/Nix storage checks")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := state.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	image, err := container.BuildAgentImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var environments []state.Environment
	for range 2 {
		launch, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: t.TempDir(), Owner: "test", Backend: "codex"})
		if err != nil {
			t.Fatal(err)
		}
		environments = append(environments, launch.Environment)
	}
	shared := environments[0].NixVolume
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		_, _ = docker(cleanupCtx, "rm", "--force", shared)
		for _, env := range environments {
			_, _ = docker(cleanupCtx, "volume", "rm", env.StateVolume)
		}
		for _, name := range []string{shared, shared + "-unrelated"} {
			_, _ = docker(cleanupCtx, "volume", "rm", name)
		}
	})
	prepare := func(env state.Environment) error {
		unlock, err := store.LockStorage(ctx)
		if err != nil {
			return err
		}
		defer unlock()
		return Prepare(ctx, store, env)
	}
	// Independent launchers must serialize initial provisioning and reuse the daemon.
	results := make(chan error, 2)
	for _, env := range environments {
		go func() { results <- prepare(env) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	client := func(env state.Environment, script string) (string, error) {
		args := append([]string{"run", "--rm"}, Mounts(env)...)
		args = append(args, "--volume", env.StateVolume+":/home/pact/.codex", "--env", "HOME=/home/pact",
			"--env", "HOST_UID="+strconv.Itoa(os.Getuid()), "--env", "HOST_GID="+strconv.Itoa(os.Getgid()),
			"--entrypoint", "/bin/bash", image, "-euc",
			`usermod --non-unique --uid "$HOST_UID" --gid "$HOST_GID" pact; chown -R pact /home/pact; exec gosu pact /bin/bash -euc "$1"`,
			"test-client", `export PATH=/opt/pact/tools/bin:$PATH; `+script)
		return docker(ctx, args...)
	}
	// An already-running container must see new tools through the stable profile.
	live := shared + "-live"
	liveArgs := append([]string{"run", "--detach", "--name", live}, Mounts(environments[0])...)
	liveArgs = append(liveArgs, "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--entrypoint", "/bin/bash", image, "-ec", "exec sleep infinity")
	if _, err := docker(ctx, liveArgs...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = docker(cleanupCtx, "rm", "--force", live)
	})
	liveCommand := func(script string) (string, error) {
		return docker(ctx, "exec", live, "/bin/bash", "-ec", "export PATH=/opt/pact/tools/bin:$PATH; "+script)
	}
	if _, err := liveCommand("test -r /etc/codex/skills/graphene-stacked-prs/SKILL.md; ! command -v pact-live-tool"); err != nil {
		t.Fatal(err)
	}
	// Build a real additional toolset, keeping the existing commands available.
	cmd := exec.CommandContext(ctx, "docker", "exec", "--interactive", shared, "nix", "--store", "daemon", "build",
		"--impure", "--file", "-", "--no-link", "--profile", "/nix/var/nix/profiles/pact-tools")
	cmd.Stdin = strings.NewReader(`
let tools = builtins.storePath /nix/var/nix/profiles/pact-tools; in
builtins.derivation {
  name = "pact-live-tools";
  system = builtins.currentSystem;
  builder = "${tools}/bin/bash";
  args = [ "-ec" ''
    ${tools}/bin/mkdir -p "$out/bin"
    ${tools}/bin/ln -s ${tools}/bin/* "$out/bin/"
    printf '#!${tools}/bin/bash\nprintf live\n' > "$out/bin/pact-live-tool"
    ${tools}/bin/chmod +x "$out/bin/pact-live-tool"
  '' ];
}`)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build live toolset: %v: %s", err, output)
	}
	if output, err := liveCommand("pact-live-tool"); err != nil || output != "live" {
		t.Fatalf("running agent did not see the new tool: %v: %s", err, output)
	}
	if _, err := docker(ctx, "exec", shared, "ln", "-s", "/nix/var/nix/pact-skills/graphene-stacked-prs", "/nix/var/nix/pact-skills/pact-stale"); err != nil {
		t.Fatal(err)
	}
	if err := prepare(environments[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := liveCommand("! command -v pact-live-tool; test ! -e /etc/codex/skills/pact-stale; codex --version"); err != nil {
		t.Fatalf("launch did not reconcile its pinned toolset: %v", err)
	}
	if _, err := docker(ctx, "rm", "--force", live); err != nil {
		t.Fatal(err)
	}
	build := `
if test -e /home/pact/.codex/history; then
  test "$(cat /home/pact/.codex/history)" = "$PACT_NIX_ROOTS"
fi
printf '%s' "$PACT_NIX_ROOTS" > /home/pact/.codex/history
if touch /nix/store/forbidden 2>/dev/null; then exit 1; fi
mkdir -p /tmp/project
cd /tmp/project
printf shared > message
cat > default.nix <<'NIX'
let tools = builtins.storePath /opt/pact/tools; in
builtins.derivation {
  name = "pact-source-check";
  system = builtins.currentSystem;
  src = ./.;
  builder = "${tools}/bin/bash";
  args = [ "-ec" ''
    if ${tools}/bin/touch /nix/var/nix/gcroots/forbidden 2>/dev/null; then exit 1; fi
    ${tools}/bin/mkdir -p "$out/bin"
    printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$( ${tools}/bin/cat "$src/message" )" > "$out/bin/hello"
    ${tools}/bin/chmod +x "$out/bin/hello"
  '' ];
}
NIX
nix build --impure --file default.nix --out-link "$PACT_NIX_ROOTS/package" >&2
"$PACT_NIX_ROOTS/package/bin/hello"
readlink -f "$PACT_NIX_ROOTS/package"
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
	packagePath := path(first.output)
	if packagePath != path(second.output) || !strings.Contains(first.output, "shared\n") {
		t.Fatalf("clients did not reuse and execute one package: %s\n%s", first.output, second.output)
	}
	if _, err := docker(ctx, "exec", shared, "nix-store", "--store", "daemon", "--gc"); err != nil {
		t.Fatal(err)
	}
	if _, err := docker(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	for _, env := range environments {
		if err := prepare(env); err != nil {
			t.Fatal(err)
		}
		output, err := client(env, `test "$(cat /home/pact/.codex/history)" = "$PACT_NIX_ROOTS"; "$PACT_NIX_ROOTS/package/bin/hello"`)
		if err != nil || !strings.Contains(output, "shared") {
			t.Fatalf("state/root lost after container replacement and GC: %v: %s", err, output)
		}
	}
	// A service revision mismatch must leave the existing daemon untouched.
	oldImage := shared + "-old-daemon"
	if _, err := docker(ctx, "commit", "--change", "ENV PACT_TEST_REVISION=old", shared, oldImage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = docker(cleanupCtx, "rm", "--force", shared)
		_, _ = docker(cleanupCtx, "image", "rm", oldImage)
	})
	if _, err := docker(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	oldID, err := docker(ctx, "run", "--detach", "--name", shared, "--mount", "type=volume,src="+shared+",dst=/nix", oldImage)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepare(environments[0]); err == nil || !strings.Contains(err.Error(), "incompatible Nix daemon") {
		t.Fatalf("daemon mismatch was accepted: %v", err)
	}
	if id, err := docker(ctx, "inspect", "--format", "{{.Id}}", shared); err != nil || id != oldID {
		t.Fatalf("incompatible daemon was replaced: %s, %v", id, err)
	}
	if _, err := docker(ctx, "exec", shared, "nix", "--store", "daemon", "store", "info"); err != nil {
		t.Fatalf("incompatible daemon was interrupted: %v", err)
	}
	// Simulate an intentional Docker wipe: the catalogue and private state remain.
	if _, err := docker(ctx, "rm", "--force", shared); err != nil {
		t.Fatal(err)
	}
	if _, err := docker(ctx, "volume", "rm", shared); err != nil {
		t.Fatal(err)
	}
	for _, env := range environments {
		if err := prepare(env); err != nil {
			t.Fatalf("missing shared cache was not recreated: %v", err)
		}
		if _, err := client(env, `test "$(cat /home/pact/.codex/history)" = "$PACT_NIX_ROOTS"; test ! -e "$PACT_NIX_ROOTS/package"; codex --version`); err != nil {
			t.Fatalf("cache recovery lost private state or tools: %v", err)
		}
		if _, err := client(env, build); err != nil {
			t.Fatalf("project dependency did not rebuild: %v", err)
		}
	}
	// Established private state must never be replaced with an empty volume.
	if _, err := docker(ctx, "volume", "rm", environments[0].StateVolume); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(ctx, store, environments[0]); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("missing established state was silently recreated: %v", err)
	}
	removed, err := Collect(ctx, store)
	if err != nil || len(removed) != 0 {
		t.Fatalf("GC reclaimed a retained session: %v, %v", removed, err)
	}
	// Exercise the physical cleanup after removing catalogue references, as a
	// future session-deletion operation would. The real FK constraints still apply.
	db, err := sql.Open("sqlite3", filepath.Join(os.Getenv("XDG_STATE_HOME"), "pact", "sessions.db")+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	forget := func(env state.Environment) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `
			DELETE FROM executions WHERE session_id IN (SELECT id FROM sessions WHERE environment_id = ?);
			DELETE FROM sessions WHERE environment_id = ?`, env.ID, env.ID); err != nil {
			t.Fatal(err)
		}
	}
	for i, env := range environments {
		forget(env)
		if i == 1 {
			// A stopped container retains storage too, even without catalogue refs.
			name := env.StateVolume + "-client"
			if _, err := docker(ctx, "create", "--name", name, "--volume", env.StateVolume+":/state", image); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				_, _ = docker(cleanupCtx, "rm", "--force", name)
			})
			removed, err = Collect(ctx, store)
			if err != nil || len(removed) != 0 {
				t.Fatalf("GC ignored a Docker container reference: %v, %v", removed, err)
			}
			if _, err := docker(ctx, "rm", name); err != nil {
				t.Fatal(err)
			}
		}
		removed, err = Collect(ctx, store)
		if err != nil || len(removed) != 1 || removed[0] != env.ID {
			t.Fatalf("unreferenced environment not reclaimed: %v, %v", removed, err)
		}
		if found, err := exists(ctx, "volume", env.StateVolume); err != nil || found {
			t.Fatalf("private volume survived GC: %v, %v", found, err)
		}
		if _, err := docker(ctx, "exec", shared, "test", "!", "-e", Roots+env.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := docker(ctx, "exec", shared, "nix-store", "--store", "daemon", "--gc"); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := client(environments[1], `"$PACT_NIX_ROOTS/package/bin/hello"`); err != nil {
				t.Fatalf("removing one environment lost another's package: %v", err)
			}
		}
	}
	if _, err := docker(ctx, "exec", shared, "test", "!", "-e", packagePath); err != nil {
		t.Fatalf("dropping the last environment root did not release its package: %v", err)
	}
	if err := owned(ctx, "volume", shared, shared); err != nil {
		t.Fatalf("shared store was reclaimed: %v", err)
	}
	// An abandoned partial provision is eligible, but its name alone never
	// authorizes deleting an unlabelled volume, or any unrelated Docker resource.
	launch, err := store.CreateLaunch(ctx, state.LaunchOptions{Workspace: t.TempDir(), Owner: "test", Backend: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	env := launch.Environment
	environments = append(environments, env)
	for _, name := range []string{env.StateVolume, shared + "-unrelated"} {
		if _, err := docker(ctx, "volume", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	forget(env)
	if _, err := Collect(ctx, store); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("GC accepted an unowned volume: %v", err)
	}
	if _, err := docker(ctx, "volume", "rm", env.StateVolume); err != nil {
		t.Fatal(err)
	}
	if err := volume(ctx, env.StateVolume, env.ID, false); err != nil {
		t.Fatal(err)
	}
	if removed, err := Collect(ctx, store); err != nil || len(removed) != 1 {
		t.Fatalf("partial provision not reclaimed: %v, %v", removed, err)
	}
	if found, err := exists(ctx, "volume", shared+"-unrelated"); err != nil || !found {
		t.Fatalf("GC reclaimed unrelated storage: %v, %v", found, err)
	}
}
