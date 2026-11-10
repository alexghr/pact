#!/usr/bin/env bash
set -euo pipefail

skills=$1
extensions=$2
hostRelay=$3
gitIdentity=$4
shift 4

ln -sfn "$skills" /etc/pi/skills
ln -sfn "$extensions" /etc/pi/extensions.json

if [[ -n "${PACT_MCP_SOCKET:-}" ]]; then
  source "$hostRelay"
  start_host_relay "$PACT_MCP_SOCKET" 8765
fi

if [[ -n "${PACT_GIT_NAME:-}${PACT_GIT_EMAIL:-}${PACT_GIT_URL:-}" ]]; then
  source "$gitIdentity"
fi

cd /home/pact/workspace
exec "$@"
