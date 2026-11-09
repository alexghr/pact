#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" == "0" ]]; then
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
  chown pact:"$pact_group" /home/pact /etc/pi
  if [[ -n "${PACT_AGENT_STATE_DIR:-}" ]]; then
    mkdir -p "$PACT_AGENT_STATE_DIR"
    if [[ ! -e "$PACT_AGENT_STATE_DIR/settings.json" ]]; then
      cp /opt/pact/pi-settings.json "$PACT_AGENT_STATE_DIR/settings.json"
    fi
    chown pact:"$pact_group" "$PACT_AGENT_STATE_DIR"
    # Host authentication and model snapshots are read-only mounts.
    shopt -s dotglob nullglob
    for entry in "$PACT_AGENT_STATE_DIR"/*; do
      if [[ "$entry" != "${PACT_AGENT_AUTH_FILE:-}" && "$entry" != "${PACT_AGENT_MODELS_FILE:-}" ]]; then
        chown -R pact:"$pact_group" "$entry"
      fi
    done
  fi

  exec gosu pact "$0" "$@"
fi

exec nix shell path:/opt/pact/toolset#pact-tools --no-update-lock-file --command pact-start "$@"
