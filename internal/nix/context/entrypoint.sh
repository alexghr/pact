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

  if [[ "${PACT_REFRESH_AUTH:-}" == "1" && ! -f /opt/pact/host-auth.json ]]; then
    echo "Error: --refresh-auth requires a file-based host Codex login." >&2
    exit 1
  fi
  if [[ -f /opt/pact/host-auth.json && ( ! -f "$CODEX_HOME/auth.json" || "${PACT_REFRESH_AUTH:-}" == "1" ) ]]; then
    install --owner=pact --group="$pact_group" --mode=0600 \
      /opt/pact/host-auth.json "$CODEX_HOME/auth.json"
    echo "==> Loaded session authentication from the host Codex login." >&2
  fi

  exec gosu pact "$0" "$@"
fi

export PATH="/opt/pact/tools/bin:$PATH"
if [[ -z "${PACT_NIX_ROOTS:-}" || ! -d "$PACT_NIX_ROOTS" ]]; then
  echo 'Error: missing managed environment roots; run pact environments repair SESSION_ID on the host.' >&2
  exit 1
fi
# Profiles must resolve to the same path in the daemon's filesystem namespace.
mkdir -p "$PACT_NIX_ROOTS/profiles" "$HOME/.local/state/nix"
ln -sfn "$PACT_NIX_ROOTS/profiles" "$HOME/.local/state/nix/profiles"
ln -sfn "$PACT_NIX_ROOTS/profiles/profile" "$HOME/.nix-profile"

if [[ "${1:-}" == "--list-threads" ]]; then
  exec pact-agent list
fi

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

shared_args=(
  --config 'approval_policy="never"'
  --config "model=\"$model\""
  --config 'sandbox_mode="danger-full-access"'
  --config "developer_instructions='''$(< /opt/pact/agent-instructions.md)'''"
)
if [[ -n "$effort" ]]; then
  shared_args+=(--config "model_reasoning_effort=\"$effort\"")
fi
shared_args+=("$@")
cd /home/pact/workspace
if [[ -n "$prompt" ]]; then
  exec codex "${shared_args[@]}" exec --json --skip-git-repo-check --ignore-user-config "$prompt"
fi
exec pact-agent run "${shared_args[@]}" --
