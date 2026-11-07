#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
packages=$(go list -f '{{.Name}} {{.Dir}}' ./...)

failed=0
while read -r package directory; do
  directory=${directory#"$PWD/"}
  case "$package:$directory" in
    main:cmd/*) ;;
    main:*)
      printf 'Executable package must be under cmd/: %s\n' "$directory" >&2
      failed=1
      ;;
    *:internal/*) ;;
    *)
      printf 'Implementation package must be under internal/: %s\n' "$directory" >&2
      failed=1
      ;;
  esac
done <<< "$packages"
exit "$failed"
