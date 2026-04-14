#!/usr/bin/env bash
# Shared logic for all AI tool entrypoints. Source this, don't execute it.
#
# Provides:
#   - UID/GID alignment (call align_uid at the top of your entrypoint)
#   - parse_args: sets BRANCH, BASE_BRANCH, PROMPT, SHELL_MODE, RESUME_MODE, SESSION_ID
#   - setup_repo: clones/fetches and checks out the working branch
#   - cleanup_repo: commits any uncommitted changes after the tool finishes

set -euo pipefail

REPO_DIR="/workspaces/aztec-packages"
REPO_URL="https://github.com/AztecProtocol/aztec-packages.git"
BRANCH=""
BASE_BRANCH="next"
TASK=""
PROMPT=""
PROMPT_FILE=""
SHELL_MODE=false
RESUME_MODE=""
SESSION_ID=""

# Align container UID/GID to host user, then re-exec the caller as aztec-dev
align_uid() {
  if [[ "$(id -u)" == "0" && -n "${HOST_UID:-}" ]]; then
    usermod -u "$HOST_UID" aztec-dev 2>/dev/null || true
    groupmod -g "${HOST_GID:-$HOST_UID}" aztec-dev 2>/dev/null || true
    chown -R aztec-dev:aztec-dev /home/aztec-dev 2>/dev/null || true
    exec gosu aztec-dev "$0" "$@"
  fi
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case $1 in
      --branch) BRANCH="$2"; shift 2 ;;
      --base) BASE_BRANCH="$2"; shift 2 ;;
      --prompt-file) PROMPT_FILE="$2"; shift 2 ;;
      --continue) RESUME_MODE="continue"; shift ;;
      --resume) RESUME_MODE="resume"; SESSION_ID="$2"; shift 2 ;;
      --shell) SHELL_MODE=true; shift ;;
      --help|-h) usage; exit 1 ;;
      -*) echo "Unknown option: $1"; usage; exit 1 ;;
      *) TASK="$1"; shift ;;
    esac
  done

  # Build the prompt from task or file
  if [[ -n "$PROMPT_FILE" ]]; then
    PROMPT=$(cat "$PROMPT_FILE")
  elif [[ -n "$TASK" ]]; then
    PROMPT="$TASK"
  fi
}

setup_repo() {
  # Clone or update
  if [[ -d "$REPO_DIR/.git" ]]; then
    echo "==> Updating existing repo..."
    cd "$REPO_DIR"
    git fetch origin
  else
    echo "==> Cloning repo (this may take a while the first time)..."
    git clone "$REPO_URL" "$REPO_DIR"
    cd "$REPO_DIR"
  fi

  # Set up working branch
  if [[ -z "$BRANCH" ]]; then
    BRANCH="agent/$(date +%Y%m%d-%H%M%S)"
    git checkout -b "$BRANCH" "origin/$BASE_BRANCH"
    echo "==> Branch: $BRANCH (new, based on $BASE_BRANCH)"
  elif git rev-parse "origin/$BRANCH" &>/dev/null; then
    git checkout -B "$BRANCH" "origin/$BRANCH"
    echo "==> Branch: $BRANCH (from remote)"
  else
    git checkout -b "$BRANCH" "origin/$BASE_BRANCH"
    echo "==> Branch: $BRANCH (new, based on $BASE_BRANCH)"
  fi
}

cleanup_repo() {
  if [[ -n "$(git status --porcelain)" ]]; then
    echo ""
    echo "==> Warning: uncommitted changes found. Committing them now."
    git add -A
    git commit -m "agent: uncommitted work from autonomous run"
  fi

  echo ""
  echo "==> Done. Results are in the session's workspace directory on the host."
  echo "==> Branch: $BRANCH"
}
