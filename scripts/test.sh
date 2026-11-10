#!/usr/bin/env bash
set -euo pipefail

out=$(mktemp)
trap 'rm "$out"' EXIT

go test ./... -v | tee $out

if [[ -n "${CI:-}" ]]; then
  skipped=$(grep -c "SKIP" "$out" || true)
  if [[ "$skipped" != "0" ]]; then
    printf '\nAll tests must run; skipped tests: %d\n' "$skipped" >&2
    exit 1
  else
    exit 0
  fi
fi

