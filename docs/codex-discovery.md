# Codex integration

Pact currently supports Codex. This package maps Pact run settings and an opaque
backend reference to native Codex operations. It does not own the session
catalogue, select recent conversations, or synchronize a native thread list.

## Initial identity and resume

A prompted run uses `codex exec --json`; `thread.started` supplies the initial
native reference before later output or failure. The host saves it once as the
Pact session's `BackendRef`. The adapter validates an explicitly resumed reference
and renders assistant output separately from tool/reasoning diagnostics.

For interactive runs, `pact-agent` starts a local `codex app-server` and connects
the native TUI over its Unix WebSocket endpoint. Its observer waits for the first
active-thread status after user input, then sends a Pact-owned binding payload
to `/harness/binding`. Empty threads have no binding. The adapter emits only one
reference; native `/new` and `/resume` actions do not redirect the Pact session.
No transcript or native thread catalogue crosses the host bridge.

A prompted continuation uses `codex exec resume THREAD_ID PROMPT`. Interactive
continuation uses `codex resume THREAD_ID` through the remote TUI. Before attaching,
Pact prepares the saved thread through `thread/resume` with approval policy
`never` and sandbox `danger-full-access`, and verifies effective permissions.
This preserves Pact's container permissions when native saved permissions differ.

The same reference supports interactive and prompted continuation. There is no
native picker or most-recent fallback when a reference is present. A session
without a reference starts its first conversation. If an explicit target is
unavailable, the native operation reports an error.

## Ownership and validation

Pact owns the binding schema in `internal/harness`; native notification and CLI
JSON types stay in `internal/codex`. The adapter reports the reference through a
callback or the private bridge. Only session/state code persists it.
Authentication projection and native CLI configuration also stay in the adapter.
`hostbridge` transports ordinary HTTP handlers without interpreting these values.

`PACT_TEST_CODEX=1 ./shell go test ./internal/codex -count=1` runs real app-server
and CLI checks without an upstream model service. The tests use an unavailable
loopback provider and verify initial binding, empty-thread exclusion, stable
identity through native navigation, saved continuation history, and effective
resume permissions. They wait for native input persistence before interrupting
an offline run. Set `PACT_CODEX_BINARY` to exercise a specific installed version.

The operator also validated completed interactive and prompted continuation on
Codex 0.163.0. Pact pins the container toolset in
`internal/nix/context/tools/flake.lock`; protocol compatibility should be checked
when that pin changes.
