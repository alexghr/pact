#!/usr/bin/env bash
set -euo pipefail

source_files() {
  git ls-files -z --cached --others --exclude-standard --deduplicate "$1" |
    while IFS= read -r -d "" file; do
      if [[ -f "$file" ]]; then
        printf '%s\0' "$file"
      fi
    done
}

if [[ $# -eq 0 ]]; then
  source_files '*.nix' | xargs -0 -r nixfmt
  source_files '*.go' | xargs -0 -r gofmt -w
  exit 0
fi
if [[ $# -ne 1 || "$1" != --check ]]; then
  printf 'Usage: p-fmt [--check]\n' >&2
  exit 2
fi

source_files '*.nix' | xargs -0 -r nixfmt --check
unformatted=$(source_files '*.go' | xargs -0 -r gofmt -l)
if [[ -n "$unformatted" ]]; then
  printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
  exit 1
fi
