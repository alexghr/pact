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
  chown pact:"$pact_group" /home/pact
  if [[ -n "${PACT_AGENT_STATE_DIR:-}" ]]; then
    mkdir -p "$PACT_AGENT_STATE_DIR"
    chown pact:"$pact_group" "$PACT_AGENT_STATE_DIR"
    # The adapter's authentication file is a read-only host mount.
    shopt -s dotglob nullglob
    for entry in "$PACT_AGENT_STATE_DIR"/*; do
      if [[ "$entry" != "${PACT_AGENT_AUTH_FILE:-}" ]]; then
        chown -R pact:"$pact_group" "$entry"
      fi
    done
  fi

  exec gosu pact "$0" "$@"
fi

export PATH="/opt/pact/tools/bin:$PATH"
if [[ -z "${PACT_NIX_ROOTS:-}" || ! -d "$PACT_NIX_ROOTS" ]]; then
  echo 'Error: missing session Nix roots; restart the Pact session to prepare its storage.' >&2
  exit 1
fi
# Profiles must resolve to the same path in the daemon's filesystem namespace.
mkdir -p "$PACT_NIX_ROOTS/profiles" "$HOME/.local/state/nix"
ln -sfn "$PACT_NIX_ROOTS/profiles" "$HOME/.local/state/nix/profiles"
ln -sfn "$PACT_NIX_ROOTS/profiles/profile" "$HOME/.nix-profile"

if [[ -n "${PACT_MCP_SOCKET:-}" ]]; then
  source /opt/pact/host-relay.sh
  start_host_relay "$PACT_MCP_SOCKET" 8765
fi

if [[ -n "${PACT_GIT_NAME:-}${PACT_GIT_EMAIL:-}${PACT_GIT_URL:-}" ]]; then
  source /opt/pact/git-identity.sh
fi

cd /home/pact/workspace
exec "$@"
