#!/usr/bin/env bash
set -euo pipefail

base=${PACT_CI_BASE:-origin/main}
if ! base=$(git merge-base HEAD "$base"); then
  printf 'Cannot find dependency baseline. Fetch origin/main or set PACT_CI_BASE.\n' >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git show "$base:go.mod" > "$work/base.mod"
go mod edit -json "$work/base.mod" > "$work/base.json"
go mod edit -json > "$work/current.json"

# Include direct and indirect requirements, plus remote replacement modules.
added=$(jq -nr --slurpfile base "$work/base.json" --slurpfile current "$work/current.json" '
  def modules:
    [.Require[]?.Path, (.Replace[]? | select((.New.Version // "") != "") | .New.Path)] | unique;
  (($current[0] | modules) - ($base[0] | modules))[]
')
if [[ -n "$added" ]]; then
  printf 'New dependencies require review (compared with %s):\n%s\n' "$base" "$added" >&2
  exit 1
fi
printf 'No new dependencies.\n'
