# Codex integration

Pact currently supports Codex. This package maps Pact run settings and an opaque
backend reference to native Codex operations. It does not own the session
catalogue, select recent conversations, or synchronize a native thread list.

## Initial identity and resume

The adapter starts a local `codex app-server` and connects the native TUI over
its Unix WebSocket endpoint. Its observer waits for the first
active-thread status after user input, then sends a Pact-owned binding payload
to `/harness/binding`. Empty threads have no binding. The adapter emits only one
reference, which the host saves once as the Pact session's `BackendRef`;
native `/new` and `/resume` actions do not redirect the Pact session.
No transcript or native thread catalogue crosses the host bridge.

Continuation uses `codex resume THREAD_ID` through the remote TUI. Before attaching,
Pact prepares the saved thread through `thread/resume` with approval policy
`never` and sandbox `danger-full-access`, and verifies effective permissions.
This preserves Pact's container permissions when native saved permissions differ.

There is no native picker or most-recent fallback when a reference is present.
A session without a reference starts its first conversation. If an explicit target is
unavailable, the native operation reports an error.

## Ownership and validation

Pact owns `harness.Options` (model, effort, backend reference, and MCP
endpoints), the binding schema, and binding publication. Native notification and
CLI JSON types stay in `internal/codex`. The adapter reports the reference through
a callback; only session/state code persists it. `hostbridge` transports ordinary
HTTP handlers without interpreting these values.

`codex.PrepareContainer` projects the host login into a temporary snapshot,
configures native state/authentication mounts, and returns snapshot cleanup to
the session. It supplies `pact-agent codex JSON_OPTIONS` as the container command.
The request carries launch intent; the host login is mounted separately.

`cmd/pact-agent` decodes the request, reads Pact's instruction text, and calls
`codex.Run`. The adapter checks native login status and translates the request
into Codex flags. It supervises the app-server, TUI, initial binding observation,
and native resume preparation. Codex's Ctrl-C handling stays inside the adapter.

The shell entrypoint only prepares user identity, private state ownership, Nix
profiles, and host Git/HTTP services before executing the supplied command.
Adapters supply `PACT_AGENT_STATE_DIR` and `PACT_AGENT_AUTH_FILE` so this bootstrap
can prepare writable state without changing a read-only login snapshot.

`PACT_TEST_CODEX=1 ./shell go test ./internal/codex -count=1` runs real app-server
and CLI checks without an upstream model service. The tests use an unavailable
loopback provider and verify initial binding, empty-thread exclusion, stable
identity through native navigation, saved continuation history, and effective
resume permissions. They prepare a saved thread through the same app-server
resume operation used by the TUI and verify unavailable-target failures.
Set `PACT_CODEX_BINARY` to exercise a specific installed version.

Pact pins the container toolset in `internal/images/context/agent/env/flake.lock`;
protocol compatibility should be checked when that pin changes.
