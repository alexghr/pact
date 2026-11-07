#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
base=${PACT_CI_BASE:-origin/main}
if ! base=$(git merge-base HEAD "$base"); then
  printf 'Cannot find test baseline. Fetch origin/main or set PACT_CI_BASE.\n' >&2
  exit 1
fi

added=$(git diff --no-renames --diff-filter=A --name-only "$base" -- ':(glob)cmd/**/*_test.go')
if [[ -z "$added" ]]; then
  printf 'No new tests under cmd/.\n'
  exit 0
fi

printf 'New tests under cmd/ need justification; prefer testing application logic in internal/:\n%s\n' "$added" >&2
exit 1
