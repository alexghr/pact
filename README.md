# Pact

Pact is an agent isolation solution.

## Git

Configure a git identity complete with SSH key in in `~/.config/pact/config.json` for the agent the use. This gives the agent complete access to the SSH key!

## MCP

Configure MCP forwarding with named entries under `mcp` in your Pact config. See
[the MCP bridge documentation](docs/mcp-bridge.md) for `url`, `token`, and `tokenFile`.
