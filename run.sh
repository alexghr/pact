#!/usr/bin/env bash
set -euo pipefail

workspace="$(realpath "$1")"
prompt="$2"
model="${3:-gpt-5.6-sol}"
effort="${4:-low}"

docker build \
  --tag pact-codex \
  --file ./ubuntu/Dockerfile \
  ./ubuntu

docker run --rm \
  --volume "$workspace:/home/pact/workspace" \
  --volume pact-codex-state:/home/pact/.codex \
  --volume "$HOME/.codex/auth.json:/opt/pact/host-auth.json:ro" \
  --env "HOST_UID=$(id -u)" \
  --env "HOST_GID=$(id -g)" \
  pact-codex \
  "$prompt" \
  "$model" \
  "$effort"
