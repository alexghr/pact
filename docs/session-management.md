# Pact sessions

A Pact session binds a workspace and private agent state to one initial native
conversation. Pact owns the session ID, saved backend reference, launch settings,
capabilities, storage references, and execution outcomes. The backend owns its
conversation format and interprets the opaque `BackendRef`.

## Lifecycle

`cmd/pact` parses flags before setup and sends start, resume, or attach requests
to the daemon. `pact daemon` opens the host catalogue and calls
`session.PrepareNix(ctx)` to start or reuse the fixed shared Nix service. It passes
this runtime handle to `session.New` or `session.Restore` and runs
`StartTerminal`, followed by `Cleanup`. Constructors allocate or restore
persistent resources. `StartTerminal` owns transient service startup and terminal
execution. Config loading resolves `databasePath` and `workspaceRoot` and
validates their layout.
`session.New` receives the configured root for managed allocation.
`session.OpenStore` creates the catalogue parent directory;
`state.Open` accepts an explicit database path and applies migrations.

`session.Options` contains the workspace, initial title, backend, model, effort,
current config path, and capabilities. `Capabilities` currently contains `DIND`
and `HostNetwork`. Restoring a session retains its saved settings; it uses current
host credentials and service endpoints.

The daemon selects `databasePath` and `workspaceRoot` at startup. Start and
resume requests load their selected config and reject storage paths that differ
from the daemon's. Another config with matching storage paths can supply Git,
MCP, and approval settings for a new execution. Reattaching a live execution
keeps the config it started with.

A new session allocates a managed directory when no workspace is supplied,
records its private agent-state volume and root key, and prepares persistent
roots in the supplied Nix store. The shared container and volume names are not
session data or catalogue entries. Restore reuses the private state and root key,
and validates the recorded workspace. It does not search native history or choose another conversation.

`StartTerminal` loads current host configuration, starts the Git broker and
private HTTP bridge, starts DinD when requested, and asks the backend to prepare native
state/authentication mounts and a launch request. It creates the agent container
and attaches its terminal streams. Sessions use the native terminal UI; command
line prompted execution is unavailable.

`Cleanup` removes the agent container, private DinD container/socket volume,
bridge, broker temporary files, and login snapshot, then records the outcome.
It tolerates partial startup and repeated calls. Persistent workspace files,
private agent state and the shared daemon remain available.

`running` describes attached process startup. Outcomes are `succeeded`, `failed`,
`startupFailed`, or `interrupted`. Cleanup and final bookkeeping have bounded
contexts independent of cancellation. A hard crash leaves resources and possibly
unfinished execution records for manual intervention. There are no writer locks,
resource journal, automatic recovery, or automatic prompt replay.

## Daemon attachment

Start `pact daemon` before running sessions. `pact` or `pact start` provisions a
managed session and attaches the terminal; `pact -d` or `pact start -d` (also
`--detach`) only prints its ID. `pact attach ID` joins the same running native
TUI. Ctrl-] detaches without cancelling the execution. All start and resume
requests use this supervisor.

`internal/daemon` owns an in-memory registry, input pipes, a bounded raw output
history, and one client connection per session. Session-owned input delivery
keeps a stalled native stdin from hiding a client disconnect. Its Unix socket is
under `$XDG_RUNTIME_DIR/pact-UID/`, falling back to the host temporary directory.
Docker allocates the container PTY; `internal/docker` uses HTTP attach/resize requests over
`docker system dial-stdio`, retaining Docker's context/socket selection without
requiring a host PTY or a Docker SDK.

Daemon cancellation ends its executions and waits for cleanup. There is no
restart recovery or terminal screen model. Reattachment replays at most 1 MiB
of raw output, so restoration across different terminal sizes is experimental.
The daemon prevents a resume request from creating a second execution of an
already live session. It adds no cross-daemon writer exclusion, durable input
queue, or catalogue schema. See the README for commands.

## Backend binding

Each session starts with an empty `BackendRef`. The adapter reports the first
native conversation reference; the catalogue saves it once. Later reports cannot
change the binding or choose another Pact session. The bridge handler is bound
to the host-selected session ID; the container supplies only an opaque reference.

For Codex, the adapter observes the first globally broadcast active-thread
status after user input. Empty TUI threads do not become resume targets. Later
native `/new` or `/resume` navigation stays within Codex and leaves Pact's binding
unchanged. See [Codex integration](codex-discovery.md).

`pact resume [ID]` resumes the stored reference. Omitting the ID selects the Pact
session with the most recently started execution across all workspaces, ordered
by execution ID rather than finish time. Reattachment does not change that
order. An empty catalogue reports an error.
An already live session reattaches to its native TUI; otherwise the daemon
restores it and starts a new execution. `--detach` resumes without attaching.
An empty reference starts the session's first conversation. A missing or
unavailable saved reference produces a backend error, rather than selecting
another native conversation.

`pact sessions list` shows Pact IDs, initial titles, execution outcomes, and
catalogue update times. `show ID` also exposes the backend and saved reference.
`--json` supports automation; `--workspace PATH` filters listings. Native thread
listings, combined `ID:THREAD_ID` targeting, and native-title synchronization have
been removed.

## Package boundaries

- `internal/session`: lifecycle, workspace allocation, path and identity checks,
  explicit directory removal, persistent storage preparation, resource naming,
  Nix and DinD startup, host service wiring, and outcome recording.
- `internal/daemon`: execution ownership, start/resume/attach requests, terminal
  input and output, and cleanup after execution or daemon cancellation.
- `internal/state`: SQLite connections, migrations, transactions, and record
  queries/updates for sessions, workspaces, storage, executions, and bindings.
  It does not inspect paths or manage directories.
- `internal/harness`: Pact-owned launch intent, binding payload, and binding
  publication. Native protocol shapes stay inside the backend adapter.
- `internal/codex`: native state/authentication mounts, login projection, CLI
  invocation, interactive supervision/resume preparation, and binding observation.
  It does not import the catalogue.
- `internal/hostbridge`: private HTTP transport, routing, access control, and
  listener lifetime. It does not interpret backend payloads.
- `internal/gitbroker` and `internal/httpproxy`: host credential services.
- `internal/docker` and `internal/images`: Docker operations and embedded image inputs.

The runtime holds one persisted session record, including its workspace, storage,
and current execution ID. There is no separate launch bundle or environment
entity/package. Storage properties live on sessions. The retained storage key identifies existing Docker labels and Nix
root paths; it has no independent lifecycle. Old physical root paths retain the
`environments` directory component for compatibility.

## Authentication and boundaries

The host login snapshot includes only native ChatGPT login fields, with the
refresh token cleared. Its owner-only temporary file is mounted read-only over
the private native state volume. Session cleanup removes the host copy.
Git private keys and upstream MCP credentials remain on the host behind the
bridge. The Nix daemon has no workspace, private agent state, or credential mounts.
DinD shares only the workspace and its private socket volume.

The configured managed root cannot contain the catalogue or Pact config.
Explicit workspaces are chosen by the operator without host-path exclusion
checks. Back up catalogue, workspace files, and private native volumes together. Missing established private
state requires restoration; a missing shared package cache can be rebuilt.
See [workspaces](workspaces.md) and [Nix storage](nix-storage.md).

## Migration and validation

Migrations retain session IDs, workspace paths, private state volumes, and root
keys. Shared Nix identities are removed from the catalogue; sessions use the
runtime service supplied by the daemon. Existing sessions receive their earliest
recorded native reference, falling back to the earliest non-internal cached thread when necessary. The old
thread catalogue and environment table are removed. Stored Docker/network flags
become `settings.capabilities.dind` and `settings.capabilities.hostNetwork`.
The obsolete environment repair/GC commands have been removed; session startup
prepares its retained resources. Orphaned resources require manual removal.

Run `./shell p-ci` for formatting, vet, tests, and launcher builds. Native Codex
checks are opt-in: `PACT_TEST_CODEX=1 ./shell go test ./internal/codex -count=1`.
They use real subprocesses with an unavailable loopback provider, without
credentials or model requests. `PACT_CODEX_BINARY` selects a particular binary.
Docker/Nix checks remain opt-in under `PACT_TEST_DOCKER=1`; the storage scenario
now lives in `internal/session`.

Future backends implement the native startup, identity capture, and resume
mapping for this lifecycle. Additional backend interfaces or capabilities should
be added with actual consumers.
