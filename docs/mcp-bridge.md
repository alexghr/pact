# Host MCP bridge integrations

Pact can expose host-accessible Streamable HTTP MCP servers to the agent while
keeping their credentials in the host process. This document describes the Go
API for adding an integration and how the bridge works.

## Registering a proxy

Register proxies in [cmd/pact/mcp.go](../cmd/pact/mcp.go), then rebuild the launcher
with `./shell p-build`. There are no default registrations and no MCP settings in
`config.json`.

For example, replace the registration file with:

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/alexghr/pact/mcpproxy"
)

func mcpServers() ([]mcpproxy.Server, error) {
	proxy, err := mcpproxy.NewHTTP("http://127.0.0.1:3000/mcp", func(ctx context.Context) (http.Header, error) {
		token := os.Getenv("MY_MCP_TOKEN")
		if token == "" {
			return nil, errors.New("MY_MCP_TOKEN is required on the host")
		}
		return http.Header{"Authorization": {"Bearer " + token}}, nil
	})
	if err != nil {
		return nil, err
	}
	return []mcpproxy.Server{{Name: "example", Handler: proxy}}, nil
}
```

Set the credential in the host launcher environment, then run Pact normally.
The environment variable is not passed to Docker. The callback runs for every
upstream request and may run concurrently; it can instead read a host credential
file, call a keyring, or refresh a token using the request context. Return `nil`
as the callback for an unauthenticated upstream. Names must be unique and contain
only letters, digits, underscores, or hyphens. `Server.Handler` is a standard
`http.Handler`, so host code can also compose its own handler.

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
