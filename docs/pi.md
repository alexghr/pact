# Pi integration

Pact uses Pi's native terminal UI. `internal/pi` translates launch settings,
projects host configuration, and binds each Pact session to its initial saved
Pi conversation. Pi owns the conversation format and model selection.

## Host configuration

Every new execution snapshots `~/.pi/agent/auth.json` and
`~/.pi/agent/models.json` into private temporary files, mounted read-only over
the session's `/home/pact/.pi/agent` state volume. Missing host files become
empty snapshots, so a resumed volume cannot supply stale host configuration.
Cleanup removes the host snapshots. Host `settings.json`, extensions, packages,
and other Pi files are not copied.

API keys and OAuth access tokens are projected. Renewable OAuth tokens and
unknown credential fields are excluded. OAuth snapshots retain their native
expiry, client ID, and scopes, including the `openai` ChatGPT login format.
Pi checks for expiry before requests and attempts refresh when fewer than five
minutes remain. Container refresh cannot succeed: renew credentials on the
host, exit Pi, and run `pact resume ID` to obtain a new snapshot. Reattaching a
running execution keeps its existing snapshot. Container `/login` cannot update
the read-only login file.

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

The container toolset is pinned in
`internal/images/context/agent/env/flake.lock`. Bundled skills are supplied with
`--skill /etc/pi/skills`; project skills use Pi's normal discovery.

## Validation

Run `./shell p-ci` for the normal checks.
`PACT_TEST_PI=1 ./shell go test ./internal/pi -count=1` additionally exercises
the real Pi client against a local completion protocol endpoint, without host
credentials. It covers custom models, initial binding, native navigation,
extension reload, and saved conversation continuation. It does not verify a
live ChatGPT login or a real model server.
