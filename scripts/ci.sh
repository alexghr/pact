#!/usr/bin/env bash
set -euo pipefail

report=
if [[ $# -eq 2 && "$1" == --report && -n "$2" ]]; then
  report=$2
elif [[ $# -ne 0 ]]; then
  printf 'Usage: p-ci [--report FILE]\n' >&2
  exit 2
fi

# Invalidate an old report before starting. Only a completed suite writes JSON.
if [[ -n "$report" ]]; then
  : > "$report"
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

results='[]'
failed=0
# This is the check list for both local runs and the CI reporting matrix.
for check in lint build test deps layout; do
  printf '\nRunning %s\n' "$check"
  status=success
  reason=
  if "p-$check" 2>&1 | tee "$work/output"; then
    :
  else
    code=$?
    status=error
    failed=1
    reason="p-$check exited with status $code"
    # Keep the matrix small; full output remains in the execution job's log.
    detail=$(tail -c 2000 "$work/output")
    if [[ -n "$detail" ]]; then
      reason+=$'\n'"$detail"
    fi
  fi
  results=$(jq -cn --argjson results "$results" \
    --arg name "$check" --arg status "$status" --arg reason "$reason" \
    '$results + [{name: $name, status: $status, reason: $reason}]')
done

printf '\nCI results\n'
jq -r '.[] | "\(.name): \(.status)"' <<< "$results"
if [[ -n "$report" ]]; then
  printf '%s\n' "$results" > "$report"
fi
exit "$failed"
