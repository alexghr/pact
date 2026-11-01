# Session Nix storage

Each session has a private agent-state volume. All sessions use the Docker volume
`pact-nix` and persistent daemon container `pact-nix` in the selected Docker
context. The shared store is a rebuildable package cache, separate from retained
conversation history and workspace files.

## Resource ownership and recovery

The catalogue records each session's private state volume and ownership key before
creating its resources. Shared Nix infrastructure has no catalogue entry or
per-session reference. The launcher prepares it and passes runtime access to
new and restored sessions.

Pact does not serialize provisioning or cleanup across launcher processes; run
these operations sequentially. Builds from different sessions can run
concurrently through the daemon. The daemon has an ownership label and Docker's
`unless-stopped` restart policy. Session shutdown leaves it running.
Agents mount all of `/nix` read-only and use `NIX_REMOTE=daemon` over its Unix socket.
On Docker Desktop for Mac, the socket and named volume stay inside Docker's Linux
VM; neither is a macOS bind mount. Only the daemon container changes the store
and its database, and the daemon coordinates client builds and downloads.

The daemon image extends pinned upstream `nixos/nix` with service configuration.
Its context hash identifies the compatible daemon revision, independently of
tools and skills. Upstream Nix supplies its unprivileged build accounts. Nix
build sandbox is disabled.

## Initial agent shell

The Ubuntu agent image includes a bootstrap Nix client from Ubuntu's `nix-bin`
package and the pinned `flake.nix` and `flake.lock` from
`internal/images/context/agent/env`. After mapping the host UID/GID and preparing
private state, the entrypoint runs:

```sh
nix shell path:/opt/pact/toolset#pact-tools --no-update-lock-file --command pact-start ...
```

The client evaluates the flake inside the agent container and asks the shared
daemon to build or substitute missing outputs. Existing outputs are reused.
There is no launcher-side tool build, source copy into the daemon, or shared tool
profile. Sessions using different toolsets can coexist; starting a session does
not change tools in running agents.

`pact-start` exposes that toolset's skills at `/etc/codex/skills`, starts the host
relay and Git integration, and executes the requested agent command. This link
is private to the container. Tools and skills are pinned together. Updating them
requires rebuilding the launcher and starting a new agent container, but does
not change the daemon image.

The agent inherits the initial shell's `PATH` and can use ordinary `nix shell`,
`nix develop`, and `nix build` commands for project dependencies. Pact does not
create session GC roots or redirect user profiles into the shared volume.

## Garbage collection

Pact does not run or schedule Nix garbage collection. The daemon sets
`min-free = 0` to disable space-pressure collection. Do not run manual GC while
agents are active: the daemon's PID namespace cannot see agent processes, and
Pact does not root their dependencies. If space runs out, free space outside the
store or stop all agents before collecting it.

Private agent state and workspace files survive collection, but package
dependencies may need downloading or rebuilding on resume.

## Recovery and upgrades

Private state provisioning flags distinguish interrupted setup from established
session storage. Missing established private state volumes require restoration
from backup. Keep the catalogue and private-volume backups together, and use
the same Docker context when restarting a session.

If the shared Nix volume is absent, startup recreates it from upstream Nix's
initial store. The agent's first shell rebuilds or downloads its tools and project
dependencies as needed. This does not replace private conversation history.
Docker ownership labels identify Pact resources: the shared volume's label is
its name; a private volume's label is its session storage key.

If the daemon container's image hash differs from this Pact's expected revision,
launch stops with both image references and leaves the existing daemon alone.
Use a matching Pact or finish active sessions before a controlled daemon upgrade.
For configuration changes using the same upstream Nix base, remove **only the
daemon container** and retry with its volume. Changing the pinned upstream Nix
base additionally requires a checked store bootstrap update, since an existing
volume does not receive Docker's initial image copy again.

Older Pact releases used shared tool profiles, skill links, and session roots.
This version no longer uses them; existing links remain in an upgraded cache.
Randomly named legacy Nix containers and volumes are not adopted or removed.
Retire old resources manually after the sessions using them have stopped.

## Validation

`PACT_TEST_DOCKER=1 ./shell go test ./internal/session -run TestSharedStorage -count=1`
uses disposable real Docker volumes and embedded images. It checks initial and
nested Nix shells, source builds through the daemon, shared package reuse,
private state, read-only store mounts, daemon compatibility, and rebuilding a
deleted package cache. It removes only its own disposable resources.
