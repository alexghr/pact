#!/usr/bin/env bash

# Sourced after dropping privileges. The EXIT trap handles startup failures;
# after exec'ing Codex, Docker tears down the relay when the container exits.
start_mcp_relay() {
  local socket_path="$1" port="$2" attempt
  if [[ ! -S "$socket_path" ]]; then
    echo "Error: Pact MCP socket is missing." >&2
    return 1
  fi

  socat "TCP4-LISTEN:$port,bind=127.0.0.1,reuseaddr,fork" \
    "UNIX-CONNECT:$socket_path" &
  pact_mcp_relay_pid=$!
  trap 'kill "$pact_mcp_relay_pid" 2>/dev/null || true; wait "$pact_mcp_relay_pid" 2>/dev/null || true' EXIT

  for ((attempt = 0; attempt < 50; attempt++)); do
    if ! kill -0 "$pact_mcp_relay_pid" 2>/dev/null; then
      break
    fi
    if curl -q --noproxy '*' --fail --silent --max-time 0.2 \
      --output /dev/null "http://127.0.0.1:$port/_pact/health"; then
      return 0
    fi
    sleep 0.1
  done
  echo "Error: Pact MCP relay did not become ready." >&2
  return 1
}
