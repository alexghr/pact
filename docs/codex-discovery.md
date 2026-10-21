# Native Codex discovery

Investigation for [#4](https://forge.alexghr.me/ag/pact/issues/4), validated with
`codex-cli 0.162.1` on Linux on 19 October 2026. Pact retains `codex exec` and the
native TUI. Discovery uses the [App Server protocol](https://developers.openai.com/codex/app-server),
without reading Codex's SQLite schema, copying database files, or parsing
transcripts.

## Live observation

`pact-agent` starts a local `codex app-server`, initializes a Pact observer, and
connects the native TUI using `codex --remote`. The Unix socket carries WebSocket
frames, not newline-delimited JSON. The observer identifies itself as `pact`;
it does not impersonate a native client.

The pinned server broadcasts thread creation, name, archive, and status
notifications to initialized connections. Pact treats these as refresh hints,
then calls `thread/list` to obtain complete saved-thread metadata. In particular,
`thread/started` can describe an empty, unsaved conversation and must not alone
make it a resume target. Pact publishes small `threads` snapshots to its host
catalogue; it does not forward transcripts or token output.

Discovery does not resume or subscribe to threads. Turn/item notifications
are scoped to subscribers, and subscribing can keep idle threads loaded. Neither
is necessary for discovery. There is no dedicated notification for every resume
or TUI selection. Metadata is also refreshed at startup, normal TUI exit, and
before a later resume, so events are not the sole recovery mechanism.

Before attaching the remote TUI to a saved thread, Pact calls `thread/resume`
with `approvalPolicy: "never"` and `sandbox: "danger-full-access"`, then checks
the returned effective permissions. The TUI's ID-only resume rejoins that loaded
thread. This reconciles restrictive saved permissions without passing the CLI
overrides that remote resume rejects. Failure stops attachment.

Two initialized real clients confirmed passive start/fork/name notifications
and that the observer remained unsubscribed. The native TUI probe confirmed
remote operation with the observer attached. An empty new thread could emit a
start notification while remaining absent from `thread/list`. The first
connection's identity also influenced native origin/source metadata: a genuine
Pact observer can produce source `vscode` in this version. Pact accepts that
native classification rather than changing its client identity.

See the pinned [notification routing](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/outgoing_message.rs),
[thread lifecycle](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server/src/request_processors/thread_processor.rs),
and [Unix transport](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/app-server-transport/src/transport/unix_socket.rs).

## Saved metadata and selection

List active and archived conversations, paginate all results, include `cli`,
`exec`, `appServer`, and `vscode` sources, and do not restrict model providers.
Internal subagents are excluded. IDs are opaque; titles prefer `name` over
`preview`, and `forkedFromId` supplies optional lineage. Codex remains responsible
for restoring conversation context.

Default selection uses the protocol's `updatedAt`, with descending thread-ID
order for ties. Public timestamps have second precision. Pact converts them to
its catalogue representation without inventing finer precision. Opening an older
conversation can advance saved recency without a new message. Switching to a
still-loaded conversation may not; this is an accepted native limitation. An
empty unsaved `/new` leaves the prior saved target intact. Pact does not classify
user activity or promise to record every TUI selection.

Each Pact environment has private native state, so identical container workspace
paths do not mix sessions. Before resume and after execution, a network-isolated
Docker helper starts the bundled adapter against that state and returns only
metadata. The launcher retains execution ownership while recorded helpers run,
after stopping the agent and sidecar. The native store is writable because Codex
may repair its own metadata while listing. Pact does not edit native storage directly or transfer a
tar snapshot to the host.

Protocol errors are errors, not an empty catalogue. Previously known entries
missing from a successful refresh remain marked missing. Missing or archived
selected entries are rejected; Codex validates native conversation contents when
resuming. Saved native recency remains authoritative after a crash, including
any native persistence limitations.

## Compatibility and checks

The image pins Codex through `internal/nix/context/tools/flake.lock`. A documented
protocol is a narrower integration boundary than internal storage, but upgrades
can still change listing, notification, source, or timestamp behavior. Revalidate
against the actual new binary and existing saved state when updating the pin.

Use disposable `CODEX_HOME` state, unset inherited thread/session IDs, and keep
any copied authentication private and remove it afterward. Check two initialized
clients and the native TUI against the same server: start, fork, rename, resume,
and leave an empty `/new`. Compare notifications with saved lists, verify archive
and pagination behavior, and confirm that the observer is not subscribed. Check
view-only saved recency and second-resolution ties. Finally stop the server and
query the saved store through the isolated helper to cover missed notifications.

Automated adapter checks use the real pinned app-server, not a fake executable.
Historical Pact catalogue migrations retain their checksums for existing
installations; obsolete activity-certainty and reconciliation columns are unused.
