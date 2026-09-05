#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" == "0" ]]; then
  # optional setup
  if [[ -f /opt/pact/setup.sh ]]; then
    echo "==> Running setup script as root..." >&2
    /bin/bash /opt/pact/setup.sh >&2
  fi

  # remap container uid/gid to host to avoid sudo issues on the host
  if [[ -n "${HOST_UID:-}" ]]; then
    host_gid="${HOST_GID:-$HOST_UID}"
    # the uid/gid might already be taken inside the container (ubuntu is uid 1000 inside the container)
    if ! getent group "$host_gid" >/dev/null; then
      groupmod --gid "$host_gid" pact
    fi
    # I'm taking on some risk here by having two users with the same uid
    usermod --non-unique --uid "$HOST_UID" --gid "$host_gid" pact
  fi

  pact_group="$(id --group --name pact)"
  mkdir -p "$CODEX_HOME"
  chown pact:"$pact_group" /home/pact
  chown -R pact:"$pact_group" "$CODEX_HOME"

  if [[ -f /opt/pact/host-auth.json && ! -f "$CODEX_HOME/auth.json" ]]; then
    install --owner=pact --group="$pact_group" --mode=0600 \
      /opt/pact/host-auth.json "$CODEX_HOME/auth.json"
    echo "==> Initialized session authentication from the host Codex login." >&2
  fi

  exec gosu pact "$0" "$@"
fi

if ! codex login status >/dev/null 2>&1; then
  echo "Error: This Pact session has no usable Codex login." >&2
  echo "Create a file-based host login with Codex CLI, then retry." >&2
  exit 1
fi

exec codex app-server \
  --listen stdio:// \
  --config 'sandbox_mode="danger-full-access"' \
  --config 'web_search="disabled"' \
  --config 'mcp_servers.artifacts={ command = "node", args = ["/opt/pact/artifacts-proxy.mjs", "/opt/pact/artifacts.sock"], enabled = true, required = true, default_tools_approval_mode = "approve" }'
