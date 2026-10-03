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

  # The store belongs to this container, not to the mounted workspace.
  mkdir -p /nix/store /nix/var/nix
  chown pact:"$pact_group" /nix /nix/store
  chown -R pact:"$pact_group" /nix/var

  if [[ -f /opt/pact/host-auth.json && ! -f "$CODEX_HOME/auth.json" ]]; then
    install --owner=pact --group="$pact_group" --mode=0600 \
      /opt/pact/host-auth.json "$CODEX_HOME/auth.json"
    echo "==> Initialized session authentication from the host Codex login." >&2
  fi

  exec gosu pact "$0" "$@"
fi

# Seed and refresh image tools after dropping root. Existing project packages
# survive image upgrades; all Nix operations run as the agent user.
if ! cmp -s /opt/pact/store-registration /nix/.pact-store-registration; then
  tar --extract --file /opt/pact/store.tar --directory /nix/store \
    --skip-old-files --no-same-owner >&2
  export PATH="/opt/pact/tools/bin:$PATH"
  nix-store --load-db < /opt/pact/store-registration >&2
  mkdir -p /nix/var/nix/gcroots
  ln -sfn /opt/pact/tools /nix/var/nix/gcroots/pact-tools
  cp /opt/pact/store-registration /nix/.pact-store-registration
fi
export PATH="/opt/pact/tools/bin:$PATH"

if [[ $# -ne 3 ]]; then
  echo "Error: expected prompt, model, and effort arguments." >&2
  exit 1
fi

if ! codex login status >/dev/null 2>&1; then
  echo "Error: This Pact session has no usable Codex login." >&2
  echo "Create a file-based host login with Codex CLI, then retry." >&2
  exit 1
fi

codex_args=(--ask-for-approval never)
if [[ -n "$1" ]]; then
  codex_args+=(exec --skip-git-repo-check --ignore-user-config)
fi

codex_args+=(
  --model "$2" \
  --sandbox danger-full-access \
  --config "developer_instructions='''$(< /opt/pact/agent-instructions.md)'''" \
  --cd /home/pact/workspace
)
if [[ -n "$3" ]]; then
  codex_args+=(--config "model_reasoning_effort=\"$3\"")
fi
if [[ -n "$1" ]]; then
  codex_args+=("$1")
fi

exec codex "${codex_args[@]}"
