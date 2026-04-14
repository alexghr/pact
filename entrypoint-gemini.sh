#!/usr/bin/env bash
source /usr/local/bin/entrypoint-common.sh

align_uid "$@"

# --- Parse args ---
usage() {
  cat <<'USAGE'
Usage: [options] <task description>

Options:
  --branch <name>       Working branch name (default: agent/<timestamp>)
  --base <branch>       Base branch to start from (default: next)
  --prompt-file <path>  Read task from a file instead of CLI arg
  --shell               Drop into a shell
USAGE
}

parse_args "$@"

if $SHELL_MODE; then
  exec /bin/bash
fi

if [[ -n "$RESUME_MODE" ]]; then
  echo "Error: --continue/--resume is not supported with Gemini."
  exit 1
fi

# --- New task ---
if [[ -z "$PROMPT" ]]; then
  echo "Error: No task provided."
  usage
  exit 1
fi

if [[ -z "${GEMINI_API_KEY:-}" ]]; then
  echo "Error: GEMINI_API_KEY is not set."
  echo "Get a key at: https://aistudio.google.com/apikey"
  exit 1
fi

setup_repo
echo "==> Task: $PROMPT"
echo ""

gemini -p "$PROMPT" -y

cleanup_repo
