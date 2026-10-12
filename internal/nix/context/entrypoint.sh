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

if [[ $# -lt 3 ]]; then
  echo "Error: expected prompt, model, effort, and optional Codex arguments." >&2
  exit 1
fi
prompt="$1"
model="$2"
effort="$3"
shift 3

if ! codex login status >/dev/null 2>&1; then
  echo "Error: This Pact session has no usable Codex login." >&2
  echo "Create a file-based host login with Codex CLI, then retry." >&2
  exit 1
fi

if [[ -n "${PACT_MCP_SOCKET:-}" ]]; then
  source /opt/pact/host-relay.sh
  start_host_relay "$PACT_MCP_SOCKET" 8765
fi

if [[ -n "${PACT_GIT_NAME:-}${PACT_GIT_EMAIL:-}${PACT_GIT_URL:-}" ]]; then
  source /opt/pact/git-identity.sh
fi

codex_args=(--ask-for-approval never)
if [[ -n "$prompt" ]]; then
  codex_args+=(exec --skip-git-repo-check --ignore-user-config)
fi

codex_args+=(
  --model "$model" \
  --sandbox danger-full-access \
  --config "developer_instructions='''$(< /opt/pact/agent-instructions.md)'''" \
  --cd /home/pact/workspace
)
if [[ -n "$effort" ]]; then
  codex_args+=(--config "model_reasoning_effort=\"$effort\"")
fi
codex_args+=("$@")
if [[ -n "$prompt" ]]; then
  codex_args+=("$prompt")
fi

exec codex "${codex_args[@]}"
