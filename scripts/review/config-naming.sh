#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
exec go run ./cmd/check-config-naming
