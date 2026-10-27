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
  # auth.json is a read-only snapshot, so adjust ownership only for saved state.
  chown pact:"$pact_group" "$CODEX_HOME"
  shopt -s dotglob nullglob
  for entry in "$CODEX_HOME"/*; do
    if [[ "$entry" != "$CODEX_HOME/auth.json" ]]; then
      chown -R pact:"$pact_group" "$entry"
    fi
  done

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

if [[ $# -lt 3 ]]; then
  echo "Error: expected prompt, model, effort, and optional Codex arguments." >&2
  exit 1
fi
prompt="$1"
model="$2"
effort="$3"
shift 3

if ! codex login status >/dev/null 2>&1; then
  echo "Error: The host Codex login snapshot is unusable." >&2
  echo "Renew the host's ChatGPT login with Codex CLI, then retry." >&2
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
  if [[ -n "${PACT_RESUME_THREAD:-}" ]]; then
    exec codex "${shared_args[@]}" exec resume --dangerously-bypass-approvals-and-sandbox --json --skip-git-repo-check --ignore-user-config -- "$PACT_RESUME_THREAD" "$prompt"
  fi
  exec codex "${shared_args[@]}" exec --json --skip-git-repo-check --ignore-user-config -- "$prompt"
fi
if [[ -n "${PACT_RESUME_THREAD:-}" ]]; then
  exec pact-agent run "${shared_args[@]}" -- resume -- "$PACT_RESUME_THREAD"
fi
# New threads need explicit launch settings on the TUI too. Remote resume above
# rejoins a thread whose permissions pact-agent reconciles through the server.
exec pact-agent run "${shared_args[@]}" -- "${shared_args[@]}"
