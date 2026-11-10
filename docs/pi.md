# Pi integration

Pact uses Pi's native terminal UI. `internal/pi` translates launch settings,
projects host configuration, and binds each Pact session to its initial saved
Pi conversation. Pi owns the conversation format and model selection.

## Host configuration

Every new execution snapshots `~/.pi/agent/auth.json` and
`~/.pi/agent/models.json` into private temporary files. Models are mounted
read-only over the session's `/home/pact/.pi/agent` state volume. The access-only
authentication seed is mounted read-only at `/opt/pact/pi-auth.json` and copied
to writable native `auth.json` before Pi starts. Missing host files become empty
snapshots, so a resumed volume cannot supply stale host configuration. Cleanup
removes the host snapshots. Host `settings.json`, extensions, packages, and
other Pi files are not copied.

API keys and OAuth access tokens are projected. Renewable OAuth tokens and
unknown credential fields are excluded. OAuth credentials retain their native
expiry, client ID, and scopes, including the `openai` ChatGPT login format.
Other OAuth providers still use snapshot-only credentials: renew them on the
host and start a new execution to obtain an updated snapshot.

### OpenAI ChatGPT renewal

The daemon needs Pi 1.5 or newer available as `pi` on its host PATH. When native
Pi resolves an OpenAI OAuth credential with fewer than five minutes remaining,
Pact's extension delegates renewal to `POST /harness/openai-auth` on the existing
private host bridge. No credential or provider name is accepted from the caller.
The host runs `pi auth print-bearer-token --provider openai --min-expiry 10m`
from its Pi configuration directory, never the agent workspace. Native Pi
coordinates refreshes with other Pact sessions and host Pi processes under its
credential lock and persists rotated refresh credentials before export.

Pact privately captures secret stdout and verifies it against a complete
access-only projection with the native expiry. It retries a partial read or
concurrent host replacement once. Only the OpenAI projection crosses the bridge;
refresh tokens, unknown fields, native diagnostics and provider error bodies do
not. Responses are not cacheable. The container's native credential store
persists the reply before request-auth resolution continues. OAuth derivation,
subscription model filtering, and API-key authentication remain native; the
access token is not reclassified as an API key.

This is request-driven, not an idle renewal timer. Detached sessions renew when
they need credentials; an idle session can safely pass its expiry and renew on
its next request, including after host sleep. Live delivery updates container-local
state rather than replacing an already-mounted host file, so it does not depend
on bind-mount inode replacement or a credential-cache reload hook. OpenAI
container `/login` directs the operator to host Pi instead of creating renewable
credentials in the container.

Renewal makes at most two bridge attempts, with a one-second delay, within Pi's
native refresh deadline. Failures report host network, `pi auth check --provider
openai`, and host `/login` recovery steps without terminating Pi or replacing the
conversation. Retry after host recovery; no stop/resume or reattachment is needed.
A cancelled request does not cancel an in-flight host rotation: shutdown waits
for its persistence, bounded by a one-minute host export deadline. If a slow
export outlives the container's wait, a subsequent request can use the persisted
host credential. No OpenAI refresh-token grant runs inside the container, even
with a missing or unavailable bridge.

### Custom models

Pact copies `models.json` unchanged, preserving provider URLs, compatibility
options, sampling settings, and custom model fields. `--model` is an opaque
string passed directly to Pi, for example:

```sh
pact --model local-llama/gpt-oss-20b --hostnet
```

`--hostnet` is needed for a provider URL bound to host loopback, such as
`http://127.0.0.1:8080/v1`. On Docker Desktop, enable host networking in its
settings first. Otherwise configure a URL reachable from the container.
Environment references and commands in Pi configuration resolve inside the
container; host environment variables and helper commands are not copied.
Without `--model`, Pi chooses its model. `--effort` maps to Pi's `--thinking`.

## Binding and resume

`pact-agent pi JSON_OPTIONS` runs Pi with an invocation-specific extension.
The extension registers configured MCP endpoints and reports the first durable
Pi session file through `/harness/binding`. Empty conversations have no saved
file and do not become resume targets. The host persists the reference once;
native `/new`, `/resume`, and `/reload` do not redirect that Pact session.
No transcript or native session catalogue crosses the bridge.

Resume supplies the saved file with Pi's `--session`. An unavailable or empty
file fails instead of selecting another conversation. Private Pi state persists
in the session volume. Existing Codex sessions retain their records and files
but cannot resume with Pi; use `pact -w PATH` to start a new Pi session in their
workspace.

## Initial task input

`pact start -d -p TEXT` (also `pact -d --prompt TEXT`) sends an initial user
message through the invocation's extension using `pi.sendUserMessage()` on
`session_start` with reason `startup`. The daemon still owns the ordinary native
TUI and returns the Pact ID without waiting for the turn. Attachment does not
submit input, replace the runtime, or cancel work.

The prompt is transient execution input, not a retained setting. New executions
created by `pact resume` do not carry it; native `/new`, `/resume`, `/fork`, and
`/reload` do not resubmit it either. Text is passed as a user message without CLI
file expansion or slash-command/template expansion. Pi's input hooks still apply.
Startup and submission errors remain visible in the allocated session's terminal;
attach to inspect them. Process failures also retain the normal execution outcome.
There is no durable prompt queue, cross-request deduplication, or crash replay.

The container toolset is pinned in
`internal/images/context/agent/env/flake.lock`. Bundled skills are supplied with
`--skill /etc/pi/skills`; project skills use Pi's normal discovery.

## Validation

Run `./shell p-ci` for all checks, including the pinned Pi 1.5 client against
local protocol endpoints without real host credentials. It covers custom models,
initial task input, binding, native navigation without resubmission, and saved
conversation continuation. On Linux it also runs two live Pi processes through
the actual Unix host bridge and shipped relay, with native host OAuth
renewal against a minimal HTTPS token endpoint. It checks cross-process refresh
coordination, rotated-credential persistence, access-only delivery through native
request authentication, delayed replies, exhausted validity, bounded failure and
recovery, conversation continuity, and cancellation during rotation. The
development Pi pin matches the shipped container pin.

These are deterministic protocol checks, not live OAuth validation. Docker
Desktop/macOS file sharing, actual TUI detachment and host sleep/wake, and an
uninterrupted real OpenAI conversation across real token renewal still require
separate live validation. Do not record tokens or secret auth-command stdout in
that validation.
