#!/usr/bin/env bash
set -euo pipefail

# Nix resolves the Unix peer's UID/GID in the daemon's own user database.
if ! grep -q "^[^:]*:[^:]*:$HOST_GID:" /etc/group; then
  printf 'pact-client:x:%s:\n' "$HOST_GID" >> /etc/group
fi
if ! id "$HOST_UID" >/dev/null 2>&1; then
  printf 'pact-client:x:%s:%s:Pact client:/tmp:/bin/sh\n' "$HOST_UID" "$HOST_GID" >> /etc/passwd
fi
mkdir -p /nix/var/nix/gcroots/environments
exec nix-daemon
