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
# This is the check list for both local runs and the CI reporting matrix.
for check in lint build test deps layout config-naming docker-boundary cmd-tests; do
  printf '\nRunning %s\n' "$check"
  if "p-$check" 2>&1 | tee "$work/output"; then
    code=0
  else
    code=$?
  fi

  # All checks use the same exit-code contract.
  case "$code" in
    0) status=success ;;
    10) status=warning ;;
    *) status=error ;;
  esac

  reason=
  if [[ "$code" -ne 0 ]]; then
    # Keep the matrix small; full output remains in the execution job's log.
    reason=$(tail -c 2000 "$work/output")
  fi
  results=$(jq -cn --argjson results "$results" \
    --arg name "$check" --argjson exitCode "$code" \
    --arg status "$status" --arg reason "$reason" \
    '$results + [{name: $name, exitCode: $exitCode, status: $status, reason: $reason}]')
done

printf '\nCI results\n'
jq -r '.[] | "\(.name): \(.status)"' <<< "$results"
if [[ -n "$report" ]]; then
  printf '%s\n' "$results" > "$report"
fi
jq -e 'all(.[]; .status != "error")' <<< "$results" > /dev/null
