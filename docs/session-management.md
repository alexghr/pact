# Pact sessions

A Pact session binds a workspace and private agent state to one initial native
conversation. Pact owns the session ID, saved backend reference, launch settings,
capabilities, storage references, and execution outcomes. The backend owns its
conversation format and interprets the opaque `BackendRef`.

## Lifecycle

`cmd/pact` parses flags before setup, opens the host catalogue, and calls
`session.PrepareNix` to start or reuse shared Nix infrastructure. It then calls
`session.New` or `session.Restore`, registers `Cleanup`, and calls `Start` with
an optional prompt. Constructors allocate or restore persistent resources.
`Start` owns transient service startup and attached execution.

`session.Options` contains the workspace, initial title, backend, model, effort,
current config path, and capabilities. `Capabilities` currently contains `DIND`
and `HostNetwork`. Restoring a session retains its saved settings; it uses current
host credentials and service endpoints.

A new session allocates a managed directory when no workspace is supplied,
records its private agent-state volume and Nix storage references, and prepares
persistent roots. Restore reuses those identities and validates the recorded
workspace. It does not search native history or choose another conversation.

`Start` loads current host configuration, projects the native login, starts the
Git broker and private HTTP bridge, starts DinD when requested, creates the agent
container, and attaches its process streams. Prompted stdout is rendered by the
backend adapter. Interactive sessions use the native terminal UI.

`Cleanup` removes the agent container, private DinD container/socket volume,
bridge, broker temporary files, and login snapshot, then records the outcome.
It tolerates partial startup and repeated calls. Persistent workspace files,
private agent state, Nix roots, and the shared daemon remain available.

`running` describes attached process startup. Outcomes are `succeeded`, `failed`,
`startupFailed`, or `interrupted`. Cleanup and final bookkeeping have bounded
contexts independent of cancellation. A hard crash leaves resources and possibly
unfinished execution records for manual intervention. There are no writer locks,
resource journal, automatic recovery, or automatic prompt replay.

## Backend binding

Each session starts with an empty `BackendRef`. The adapter reports the first
native conversation reference; the catalogue saves it once. Later reports cannot
change the binding or choose another Pact session. The bridge handler is bound
to the host-selected session ID; the container supplies only an opaque reference.

For Codex, prompted runs obtain the reference from `thread.started` JSON output.
The interactive adapter observes the first globally broadcast active-thread
status after user input. Empty TUI threads do not become resume targets. Later
native `/new` or `/resume` navigation stays within Codex and leaves Pact's binding
unchanged. See [Codex integration](codex-discovery.md).

`pact resume ID` resumes the stored reference. With `--prompt` it uses prompted
continuation; otherwise it opens the native TUI. An empty reference starts the
session's first conversation. A missing or unavailable saved reference produces
a backend error, rather than selecting another native conversation.

`pact sessions list` shows Pact IDs, initial titles, execution outcomes, and
catalogue update times. `show ID` also exposes the backend and saved reference.
`--json` supports automation; `--workspace PATH` filters listings. Native thread
listings, combined `ID:THREAD_ID` targeting, and native-title synchronization have
been removed.

## Package boundaries

- `internal/session`: lifecycle, persistent storage preparation, Nix and DinD
  startup, host service wiring, and outcome recording.
- `internal/state`: host SQLite catalogue, migrations, workspace identities,
  session storage properties, settings, and immutable backend bindings.
- `internal/harness`: Pact-owned binding payload. Native protocol shapes stay
  inside the backend adapter.
- `internal/codex`: native CLI configuration, authentication projection,
  app-server notifications/resume preparation, and prompted event translation.
  It does not import the catalogue.
- `internal/hostbridge`: private HTTP transport, routing, access control, and
  listener lifetime. It does not interpret backend payloads.
- `internal/gitbroker` and `internal/httpproxy`: host credential services.
- `internal/docker` and `internal/nix`: Docker operations and embedded image inputs.

There is no separate environment entity or package. Storage properties live on
sessions. The retained storage key identifies existing Docker labels and Nix
root paths; it has no independent lifecycle. Old physical root paths retain the
`environments` directory component for compatibility.

## Authentication and boundaries

The host login snapshot includes only native ChatGPT login fields, with the
refresh token cleared. Its owner-only temporary file is mounted read-only over
the private native state volume. Session cleanup removes the host copy.
Git private keys and upstream MCP credentials remain on the host behind the
bridge. The Nix daemon has no workspace, private agent state, or credential mounts.
DinD shares only the workspace and its private socket volume.

The host catalogue stays outside every mounted workspace. Back up catalogue,
workspace files, and private native volumes together. Missing established private
state requires restoration; a missing shared package cache can be rebuilt.
Managed workspace removal is explicit and preserves native state. See
[workspaces](workspaces.md) and [Nix storage](nix-storage.md).

## Migration and validation

Migrations retain session IDs, workspace paths, physical volumes, ownership keys,
and Nix roots. Existing sessions receive their earliest recorded native reference,
falling back to the earliest non-internal cached thread when necessary. The old
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
