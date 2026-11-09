#!/usr/bin/env bash

# Sourced after dropping privileges. The EXIT trap handles startup failures;
# after exec'ing Pi, Docker tears down the relay when the container exits.
start_host_relay() {
  local socket_path="$1" port="$2" attempt
  if [[ ! -S "$socket_path" ]]; then
    echo "Error: Pact host socket is missing." >&2
    return 1
  fi

  pact_host_relay_log=$(mktemp "${TMPDIR:-/tmp}/pact-host-relay.XXXXXXXX.log") || return 1
  # Forked connections can fail while Pi owns the terminal. Keep their
  # diagnostics in the container; only startup failures belong on stderr.
  socat -lf "$pact_host_relay_log" "TCP4-LISTEN:$port,bind=127.0.0.1,reuseaddr,fork" \
    "UNIX-CONNECT:$socket_path" &
  pact_host_relay_pid=$!
  trap 'kill "$pact_host_relay_pid" 2>/dev/null || true; wait "$pact_host_relay_pid" 2>/dev/null || true; rm -f "$pact_host_relay_log"' EXIT

  for ((attempt = 0; attempt < 50; attempt++)); do
    if ! kill -0 "$pact_host_relay_pid" 2>/dev/null; then
      break
    fi
    if curl -q --noproxy '*' --fail --silent --max-time 0.2 \
      --output /dev/null "http://127.0.0.1:$port/_pact/health"; then
      return 0
    fi
    sleep 0.1
  done
  echo "Error: Pact host relay did not become ready." >&2
  cat "$pact_host_relay_log" >&2
  return 1
}
