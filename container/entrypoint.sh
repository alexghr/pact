#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" == "0" ]]; then
  # optional setup
  if [[ -f /opt/pact/setup.sh ]]; then
    echo "==> Running setup script as root..."
    /bin/bash /opt/pact/setup.sh
  fi

  # remap container uid/gid to host to avoid sudo issues on the host
  if [[ -n "${HOST_UID:-}" ]]; then
    groupmod --gid "${HOST_GID:-$HOST_UID}" pact
    usermod --uid "$HOST_UID" --gid "${HOST_GID:-$HOST_UID}" pact
  fi

  mkdir -p "$CODEX_HOME"
  chown -R pact:pact /home/pact

  if [[ -f /opt/pact/host-auth.json && ! -f "$CODEX_HOME/auth.json" ]]; then
    install --owner=pact --group=pact --mode=0600 \
      /opt/pact/host-auth.json "$CODEX_HOME/auth.json"
    echo "==> Initialized session authentication from the host Codex login."
  fi

  exec gosu pact "$0" "$@"
fi

if [[ $# -ne 3 || -z "$1" ]]; then
  echo "Error: expected prompt, model, and effort arguments." >&2
  exit 1
fi

if ! codex login status >/dev/null 2>&1; then
  echo "Error: This Pact session has no usable Codex login." >&2
  echo "Create a file-based host login with Codex CLI, then retry." >&2
  exit 1
fi

prompt="$1"
model="$2"
effort="$3"

args=(
   --skip-git-repo-check
   --ignore-user-config
   --model "$model"
   --config "model_reasoning_effort=\"$effort\""
   --sandbox danger-full-access
   --config 'web_search="disabled"'
   --cd /home/pact/workspace
)

exec codex --ask-for-approval never \
  exec "${args[@]}" "$prompt"
