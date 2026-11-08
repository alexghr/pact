#!/usr/bin/env bash
set -euo pipefail

p-fmt --check
go vet ./...

failed=0
staticcheck -checks=all ./... || failed=1

mapfile -d '' -t files < <(find cmd internal -type f -name '*.go' -print0)
# Deprecations have hint severity. gopls prints diagnostics but exits successfully.
if diagnostics=$(gopls check -severity=hint "${files[@]}"); then
  if [[ -n "$diagnostics" ]]; then
    printf '%s\n' "$diagnostics"
    failed=1
  fi
else
  printf '%s\n' "$diagnostics"
  failed=1
fi

exit "$failed"
