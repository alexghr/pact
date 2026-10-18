# Host MCP bridge integrations

Pact can expose host-accessible Streamable HTTP MCP servers to the agent while
keeping their credentials in the host process. This document describes the Go
API for adding an integration and how the bridge works.

## MCP configuration

Add named Streamable HTTP MCP endpoints to your Pact `config.json`:

```json
{
  "mcp": {
    "forgejo": {
      "url": "http://127.0.0.1:3000/mcp",
      "tokenFile": "forgejo-token"
    }
  }
}
```

`url` is the complete MCP endpoint, rather than a web or REST API URL.
Specify exactly one of `token` (the literal bearer token) or `tokenFile`.
Relative file paths resolve against the config directory; `~/` resolves against
the host user's home directory. Whitespace around tokens is trimmed. The file
is checked at startup and read again on every request to support token rotation.
Credentials remain in the host proxy and are not passed to the container.
Keep the config and token file outside the mounted workspace.

`mcp` is a map of server names to configurations. Add any number of servers using
the same fields; names may contain letters, digits, underscores, or hyphens.
Pact registers each server under its map key and forwards all tools without
filtering. Omit an entry to disable it.

## HTTP infrastructure

`internal/hostbridge` owns the private host/container HTTP listener, access
control, path routing, and cleanup. Register handlers with
`hostbridge.Server`. `internal/httpproxy` provides `NewHTTP` to forward requests
to a fixed upstream endpoint and apply host-held credentials. Neither package
interprets MCP.
`cmd/pact/mcp.go` adapts MCP configuration into these HTTP handlers; the launcher
advertises only configured MCP servers to Codex. The Git broker uses the same
bridge as an ordinary HTTP handler.

Configured MCP servers use `/mcp/<name>`. Container helpers use `/harness/git`
for Git brokering and `/harness/events` for saved-thread metadata. The wrapper
publishes these events automatically; they are not agent MCP tools. Both route
namespaces share the same listener, authentication, and lifetime.

The container socket path `/opt/pact/mcp.sock` and `PACT_MCP_SOCKET` retain their
historical names but carry all bridge HTTP traffic. `PACT_GIT_URL` and
`PACT_EVENTS_URL` supply the helper endpoints; `/_pact/health` remains the health
route. The internal relay script is `host-relay.sh`.

## Transport and lifetime

On Linux, each run creates a private Unix socket outside the workspace and mounts only
that socket into the container, read-only. A `socat` relay runs as the agent user
on container loopback port 8765. Pact supplies invocation-only Codex MCP URLs;
it does not write these registrations into the persistent Codex configuration.
The host listener and active requests close when the run ends, and the container
owns the relay's lifetime.

On macOS with Docker Desktop, Pact instead listens on host loopback and connects
through `host.docker.internal` on a random host port. A random per-session token
in the URL path protects the bridge; only this bridge URL enters the container,
never upstream credentials. The bridge strips the token before forwarding.
The Git broker is always registered, even with no configured MCP servers.

The proxy sends requests to the exact registered endpoint, including its path
and query. It discards client authorization and cookies, applies host headers,
and forwards MCP traffic without parsing messages or filtering tools. Streaming,
session headers, and request cancellation are preserved. Upstream credentials are never
included in generated container configuration or proxy error messages. Upstream
responses are passed through, so upstream servers must not echo credentials.

## Supported environment

Local Linux Docker daemons use the Unix socket bridge; macOS Docker Desktop uses
the TCP bridge. Remote daemons, Docker Desktop on Linux, stdio upstreams, and
legacy HTTP+SSE transport translation are not supported. OAuth login and credential storage
belong to the host integration. Registration code is trusted host code; do not
put credentials or their files in the mounted workspace.

## Checking an integration

Run `./shell p-ci` for the checks, including a real Unix-socket/relay streaming
test. For a container smoke test on a Linux Docker host, register a test MCP,
rebuild, and verify its tools appear with `/mcp` in an interactive Pact session.
Then run `./pact -w /path/to/workspace -p 'Use example to perform a read-only operation'`
with an appropriate tool request. After each run, check that its container has
exited and its `/tmp/pact-mcp-*` socket directory has been removed.

To check TCP connectivity through Docker Desktop using the built Pact image, run
`PACT_TEST_DOCKER=1 ./shell go test ./internal/hostbridge -run TestTCPAuthenticationAndLifetime`.
