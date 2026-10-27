# Session Nix storage

Each session has a private agent-state volume. All sessions use the Docker volume
`pact-nix` and persistent daemon container `pact-nix` in the selected Docker
context. Docker Engine and
CLI 26 or newer are required for
[volume subdirectory mounts](https://docs.docker.com/engine/release-notes/26.0/#new).

## Resource ownership and recovery

The catalogue records each session's private state volume and root key before
creating its resources. Shared Nix infrastructure has no catalogue entry or
per-session reference. The launcher prepares it and passes runtime access to
new and restored sessions.

Pact does not serialize provisioning or cleanup across launcher processes; run
these operations sequentially. Builds from different sessions can run
concurrently through the daemon. The daemon has an ownership label and Docker's
`unless-stopped` restart policy. Session shutdown leaves it running.
Agents mount `/nix` read-only and use `NIX_REMOTE=daemon` over its Unix socket.
On Docker Desktop for Mac, the socket and named volume stay inside Docker's Linux
VM; neither is a macOS bind mount. Only the daemon container changes the store
and its database, and the daemon coordinates client builds and downloads.

The daemon image extends pinned upstream `nixos/nix` with service configuration.
Its context hash identifies the compatible daemon revision, independently of
tools and skills. Upstream Nix supplies its unprivileged build accounts.
Agent entrypoints, Codex configuration, and Pact's Git/MCP integrations stay in
the agent image. The daemon has no workspace, agent-state, Git-key, or
MCP-credential mounts. Docker is the build sandbox; Nix sandboxing is disabled
inside it.

Every launch starts or reuses the daemon, copies the small pinned toolset flake
into it, and runs `nix build --store daemon` with
`--profile /nix/var/nix/profiles/pact-tools`. Nix reuses valid build outputs and
atomically switches the profile after a successful build. The agent image's
`/opt/pact/tools` resolves through this shared profile. Skills are published as
links in `/nix/var/nix/pact-skills`, exposed at `/etc/codex/skills`. Keeping that
directory stable lets Codex's watcher detect added and removed skill entries;
switching the profile symlink alone does not refresh its discovery cache.
Running agents can execute newly added tools without container or daemon
replacement. Already-running programs retain their original version.

The most recent successful launch selects the shared toolset, including when an
older Pact is launched. Nix profile generations retain earlier closures; keep
those generations while running agents may still need them. A failed tool build
leaves the existing profile available to other agents. No package archive or
toolset closure is embedded in either Docker image.

Private state provisioning flags distinguish interrupted setup from established
session storage. Missing established private state volumes require restoration
from backup. The shared Nix volume is a rebuildable cache: if it is absent,
startup creates it, realises the baseline tools, and recreates session root
directories. Previous package outputs, profiles, and roots need rebuilding.
This does not replace private conversation history. Keep the catalogue and
private-volume backups together, and use the same Docker context when restarting
a session.

Docker ownership labels identify Pact resources. The shared volume's label is
its name; a private volume's label is its session storage key.

Upgrades remove the old shared-volume catalogue entry and per-session Nix volume
references. Existing sessions keep their workspace, private agent state, and
root key, and use `pact-nix` on their next launch. Randomly named shared Nix
containers and volumes are not adopted or removed automatically; their cached
packages need rebuilding in the fixed store. Retire old resources manually once
the sessions using them have stopped.

If the daemon container's image hash differs from this Pact's expected revision,
launch stops with both image references and leaves the existing daemon alone.
Use a matching Pact or finish active sessions before a controlled daemon upgrade.
For configuration changes using the same upstream Nix base, remove **only the
daemon container** and retry with its volume. Changing the pinned upstream Nix
base additionally requires a checked store bootstrap update, since an existing
volume does not receive Docker's initial image copy again. Tool and skill changes
do not require a daemon upgrade.

## Persistent Nix roots

Each session gets `/nix/var/nix/gcroots/environments/STORAGE_KEY`.
The agent mounts only its own directory writable, at exactly the same path seen
by the daemon. `PACT_NIX_ROOTS` names that directory. The daemon can resolve these
roots after the agent container exits, independently of client PID namespaces.

Use persistent links there for dependencies that a retained session needs:

```sh
nix build .#my-package --out-link "$PACT_NIX_ROOTS/my-package"
nix develop --profile "$PACT_NIX_ROOTS/development"
```

The session's initial tools are also rooted there. A result link solely inside
the workspace, or a temporary process root from `nix shell`, cannot preserve a
package after the client exits: that path/process is outside the daemon's
namespace. Project development wrappers must put their persistent profiles or
roots under `PACT_NIX_ROOTS`; do not rely on the daemon scanning client `/proc`.
Default user profiles are linked into the session's roots directory by the
entrypoint, so profile installations remain rooted too.

Nix owns collection within the running shared store, including space-pressure
collection if configured. Pact does not schedule Nix GC. Removing a session's
roots makes its otherwise unreachable packages eligible for Nix collection.

## Cleanup and validation

Normal session cleanup retains private agent state, Nix roots, and the shared
store. There is no automatic orphan collection or environment repair/GC command.
The operator removes abandoned Docker resources explicitly. Nix GC remains a
separate operation and honors retained session roots.

`PACT_TEST_DOCKER=1 ./shell go test ./internal/session -run TestSharedStorage -count=1`
uses disposable real Docker volumes and embedded images. It checks sequential
provisioning, source builds through the daemon, shared package reuse, private
state, read-only store mounts, persistent roots, live tool updates, daemon
compatibility, and rebuilding a deleted package cache. It removes only its own
disposable resources.
