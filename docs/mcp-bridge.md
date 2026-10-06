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

## Transport and lifetime

Each run creates a private Unix socket outside the workspace and mounts only
that socket into the container, read-only. A `socat` relay runs as the agent user
on container loopback port 8765. Pact supplies invocation-only Codex MCP URLs;
it does not write these registrations into the persistent Codex configuration.
The host listener and active requests close when the run ends, and the container
owns the relay's lifetime. With no registrations, no socket or relay is created.

The proxy sends requests to the exact registered endpoint, including its path
and query. It discards client authorization and cookies, applies host headers,
and forwards MCP traffic without parsing messages or filtering tools. Streaming,
session headers, and request cancellation are preserved. Credentials are never
included in generated container configuration or proxy error messages. Upstream
responses are passed through, so upstream servers must not echo credentials.

## Supported environment

This currently requires a local Linux Docker daemon with the existing UID
mapping. Docker Desktop, remote daemons, stdio upstreams, and legacy HTTP+SSE
transport translation are not supported. OAuth login and credential storage
belong to the host integration. Registration code is trusted host code; do not
put credentials or their files in the mounted workspace.

## Checking an integration

Run `./shell p-ci` for the checks, including a real Unix-socket/relay streaming
test. For a container smoke test on a Linux Docker host, register a test MCP,
rebuild, and verify its tools appear with `/mcp` in an interactive Pact session.
Then run `./pact -w /path/to/workspace -p 'Use example to perform a read-only operation'`
with an appropriate tool request. After each run, check that its container has
exited and its `/tmp/pact-mcp-*` socket directory has been removed.
