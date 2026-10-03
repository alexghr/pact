#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: run.sh --workspace PATH --prompt TEXT [--model MODEL] [--effort EFFORT]

  -w, --workspace PATH  Workspace directory to mount (required)
  -p, --prompt TEXT     Prompt to run (required)
  -m, --model MODEL     Model to use (default: gpt-6.1-sol)
  -e, --effort EFFORT   Reasoning effort (default: low)
  -h, --help            Show this help
EOF
}

fail() {
  echo "Error: $*" >&2
  usage >&2
  exit 1
}

workspace=""
prompt=""
model="gpt-6.1-sol"
effort="low"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -w|--workspace) workspace="$2"; shift 2 ;;
    -p|--prompt) prompt="$2"; shift 2 ;;
    -m|--model) model="$2"; shift 2 ;;
    -e|--effort) effort="$2"; shift 2 ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) fail "Unexpected argument: $1" ;;
  esac
done

[[ -n "$workspace" ]] || fail "--workspace is required."
[[ -n "$prompt" ]] || fail "--prompt is required."
workspace="$(realpath "$workspace")"

docker build \
  --tag pact-codex \
  --file ./nix/Dockerfile \
  ./nix

docker run --rm \
  --volume "$workspace:/home/pact/workspace" \
  --volume pact-codex-state:/home/pact/.codex \
  --volume pact-codex-state-nix:/nix \
  --volume "$HOME/.codex/auth.json:/opt/pact/host-auth.json:ro" \
  --env "HOST_UID=$(id -u)" \
  --env "HOST_GID=$(id -g)" \
  pact-codex \
  "$prompt" \
  "$model" \
  "$effort"
