#!/usr/bin/env bash
set -euo pipefail

if [[ "${PACT_STORAGE_PROVISIONED:-false}" == true && ! -f /nix/.pact-storage ]]; then
  echo 'Shared Nix store is missing its identity; restore it from backup.' >&2
  exit 1
fi
if [[ -f /nix/.pact-storage && "$(cat /nix/.pact-storage)" != "$PACT_STORAGE_ID" ]]; then
  echo 'Shared Nix store identity does not match the catalogue.' >&2
  exit 1
fi
# Nix resolves the Unix peer's UID/GID in the daemon's own user database.
if ! grep -q "^[^:]*:[^:]*:$HOST_GID:" /etc/group; then
  printf 'pact-client:x:%s:\n' "$HOST_GID" >> /etc/group
fi
if ! id "$HOST_UID" >/dev/null 2>&1; then
  printf 'pact-client:x:%s:%s:Pact client:/tmp:/bin/sh\n' "$HOST_UID" "$HOST_GID" >> /etc/passwd
fi
mkdir -p /nix/var/nix/gcroots/environments
printf '%s\n' "$PACT_STORAGE_ID" > /nix/.pact-storage
exec nix-daemon
