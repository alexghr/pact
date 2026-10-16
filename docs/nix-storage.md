# Managed environment storage

Each session has a private agent-state volume. All environments share one
catalogue-owned Docker volume and a persistent Nix daemon. Docker Engine and
CLI 26 or newer are required for
[volume subdirectory mounts](https://docs.docker.com/engine/release-notes/26.0/#new).

## Resource ownership and recovery

The catalogue records the shared volume and each environment's private agent
state volume before Docker creates anything. The environment fields remain
backend-neutral. A storage lock beside the database serializes provisioning and
cleanup across launcher processes; the kernel releases it after a crash. It is
held during setup, never while an agent is working. Builds from different
environments can run concurrently through the daemon.

The daemon container has the shared volume's recorded name, an ownership label,
and Docker's `unless-stopped` restart policy. Session shutdown leaves it running.
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

Provisioning flags distinguish an interrupted first setup from established
storage. Retrying setup reuses the recorded names and verifies Docker ownership
labels. Missing established private state volumes cause an error requesting
restoration from backup. A missing shared Nix volume is recreated under its
recorded identity, with a notice that cached dependencies were lost. The baseline
tools are rebuilt and environment root directories are recreated as needed;
previous package outputs, profiles, and roots require rebuilding. This does not
recover deleted conversation history. Keep the catalogue and private-volume
backups together, and use the same Docker context when restarting an environment.

Run `pact environments repair SESSION_ID` to finish an interrupted provision or
check an environment using its recorded identities. It does not create another
session or execution or resume a conversation. It can rebuild a missing shared
package cache, but missing established private state must be restored under its
original name with its `me.alexghr.pact.storage` label. The shared volume's label
is its own name; a private volume's label is its environment ID.

If the daemon container's image hash differs from this Pact's expected revision,
launch stops with both image references and leaves the existing daemon alone.
Use a matching Pact or finish active sessions before a controlled daemon upgrade.
For configuration changes using the same upstream Nix base, remove **only the
daemon container** and retry with its volume. Changing the pinned upstream Nix
base additionally requires a checked store bootstrap update, since an existing
volume does not receive Docker's initial image copy again. Tool and skill changes
do not require a daemon upgrade.

## Persistent Nix roots

Every environment gets `/nix/var/nix/gcroots/environments/ENVIRONMENT_ID`.
The agent mounts only its own directory writable, at exactly the same path seen
by the daemon. `PACT_NIX_ROOTS` names that directory. The daemon can resolve these
roots after the agent container exits, independently of client PID namespaces.

Use persistent links there for dependencies that a retained environment needs:

```sh
nix build .#my-package --out-link "$PACT_NIX_ROOTS/my-package"
nix develop --profile "$PACT_NIX_ROOTS/development"
```

The environment's initial tools are also rooted there. A result link solely inside
the workspace, or a temporary process root from `nix shell`, cannot preserve a
package after the client exits: that path/process is outside the daemon's
namespace. Project development wrappers must put their persistent profiles or
roots under `PACT_NIX_ROOTS`; do not rely on the daemon scanning client `/proc`.
Default user profiles are linked into the environment's roots directory by the
entrypoint, so profile installations remain rooted too.

Nix owns collection within the running shared store, including space-pressure
collection if configured. Pact does not schedule Nix GC. Removing an environment's
roots makes its otherwise unreachable packages eligible for Nix collection.

## Explicit cleanup

`pact environments gc` removes only environments without **any** retained session
reference, including abandoned partial provisions. A finished, failed, or inactive
session still retains its environment. The current CLI has no session deletion
operation; ordinary recorded sessions are therefore never GC candidates. Do not
delete catalogue rows manually to force collection.

Cleanup shares the provisioning lock. It also preserves state volumes still
referenced by another environment or Docker container, including stopped
containers. It checks ownership labels before deleting a private state volume,
removes that environment's persistent roots through the running daemon, then
removes the environment record. An interruption leaves enough recorded identity
to retry. The daemon must be available once shared storage is provisioned; repair
or start it before retrying cleanup if it has stopped.

Cleanup never prunes Docker volumes, runs Nix GC, deletes the shared store, touches
workspace files, or deletes unrelated volumes. Shared-store reset is a separate
operator action and is not offered by this command.

## Validation

`PACT_TEST_DOCKER=1 ./shell go test ./internal/environment -run TestSharedStorage -count=1`
uses disposable real Docker volumes and the embedded image. It checks concurrent
provisioning, a project-source build through the daemon, package reuse and
execution through read-only mounts, private persistent state, retained roots
after explicit Nix collection, live tool updates and pinned-toolset reconciliation,
refusal to replace an incompatible daemon or missing private state, and recovery
after deletion of the shared package cache.
It builds the agent and daemon images if needed and removes only its own
disposable resources.
It also checks retained-session and stopped-container protection, ownership
rejection, partial-provision cleanup, preservation of unrelated volumes, and
collection after the last environment root disappears.

The layout follows Nix's [client/daemon model](https://nix.dev/manual/nix/2.34/installation/multi-user.html)
and the NixOS manual's [shared daemon container example](https://nixos.org/manual/nixos/stable/#sec-gitlab-runner).
