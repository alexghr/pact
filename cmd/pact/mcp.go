package main

import "github.com/alexghr/pact/mcpproxy"

// mcpServers is the host-owned registration point. Add integrations here and
// rebuild Pact. See docs/mcp-bridge.md for a credential callback example.
func mcpServers() ([]mcpproxy.Server, error) {
	return nil, nil
}
