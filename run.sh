#!/usr/bin/env bash
set -euo pipefail

# Convenience wrapper for running AI coding agents in isolated Docker containers.
#
# Each session is fully isolated. The repo checkout lives in a host directory
# under ./sessions/<name>/, so you can inspect and push the agent's work directly.
#
# Usage:
#   ./run.sh --name my-feature "Fix the flaky token test"
#   ./run.sh --tool gemini --name my-feature "Fix the flaky token test"
#   ./run.sh --name my-feature --continue
#   ./run.sh --list
#   ./run.sh --rm my-feature

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SESSIONS_DIR="$SCRIPT_DIR/sessions"
SESSION_NAME=""
AI_TOOL="claude"

usage() {
  cat <<'USAGE'
Usage: run.sh [--tool <tool>] [--name <session>] [agent options...] <task>
       run.sh --list
       run.sh --rm <session>
       run.sh --rm-all

Global options:
  --tool <tool>       AI tool to use: claude (default), gemini
  --name <session>    Name this session (default: session-<timestamp>)
  --list              List all sessions and their status
  --rm <session>      Delete a session's directory
  --rm-all            Delete all sessions

Agent options (passed through to entrypoint):
  --branch <name>     Working branch name
  --base <branch>     Base branch (default: next)
  --prompt-file <path> Read task from file
  --continue          Resume the most recent session (claude only)
  --resume <id>       Resume a specific session by ID (claude only)
  --shell             Drop into a shell

Examples:
  ./run.sh --name token-fix "Fix the flaky test in e2e_token.test.ts"
  ./run.sh --tool gemini --name token-fix "Fix the flaky test"
  ./run.sh --name token-fix --continue
  ./run.sh --list
  ./run.sh --rm token-fix
USAGE
  exit 1
}

# --- Session management commands ---

cmd_list() {
  echo "Sessions:"
  echo ""

  if [[ ! -d "$SESSIONS_DIR" ]]; then
    echo "  (none)"
    exit 0
  fi

  local found=false
  for dir in "$SESSIONS_DIR"/*/; do
    [[ -d "$dir" ]] || continue
    found=true
    local name
    name=$(basename "$dir")

    # Check if a container is running for this session (any tool)
    local status="stopped"
    for prefix in pact-claude-agent pact-gemini-agent; do
      if docker ps --format '{{.Names}}' | grep -q "^${prefix}-${name}$"; then
        status="running"
        break
      fi
    done

    # Show branch if repo exists
    local branch=""
    if [[ -d "$dir/workspaces/aztec-packages/.git" ]]; then
      branch=" ($(git -C "$dir/workspaces/aztec-packages" branch --show-current 2>/dev/null || echo "unknown"))"
    fi

    echo "  $name  [$status]$branch"
  done

  if ! $found; then
    echo "  (none)"
  fi

  exit 0
}

cmd_rm() {
  local name="$1"
  local session_dir="$SESSIONS_DIR/$name"

  if [[ ! -d "$session_dir" ]]; then
    echo "Session not found: $name"
    exit 1
  fi

  # Stop container if running (try both tool prefixes)
  docker rm -f "pact-claude-agent-${name}" 2>/dev/null || true
  docker rm -f "pact-gemini-agent-${name}" 2>/dev/null || true

  echo "Removing session: $name"
  rm -rf "$session_dir"
  echo "Done."
}

cmd_rm_all() {
  if [[ ! -d "$SESSIONS_DIR" ]]; then
    echo "No sessions to remove."
    exit 0
  fi

  for dir in "$SESSIONS_DIR"/*/; do
    [[ -d "$dir" ]] || continue
    local name
    name=$(basename "$dir")
    docker rm -f "pact-claude-agent-${name}" 2>/dev/null || true
    docker rm -f "pact-gemini-agent-${name}" 2>/dev/null || true
  done

  rm -rf "$SESSIONS_DIR"
  echo "All sessions removed."
  exit 0
}

# --- Parse flags ---

AGENT_ARGS=()
while [[ $# -gt 0 ]]; do
  case $1 in
    --name) SESSION_NAME="$2"; shift 2 ;;
    --tool) AI_TOOL="$2"; shift 2 ;;
    --help|-h) usage ;;
    --list) cmd_list ;;
    --rm-all) cmd_rm_all ;;
    --rm)
      [[ -z "${2:-}" ]] && { echo "Error: --rm requires a session name."; exit 1; }
      cmd_rm "$2"; exit 0
    ;;
    *) AGENT_ARGS+=("$1"); shift ;;
  esac
done

if [[ ${#AGENT_ARGS[@]} -eq 0 ]]; then
  echo "Error: No arguments provided."
  usage
fi

# Default session name
if [[ -z "$SESSION_NAME" ]]; then
  SESSION_NAME="session-$(date +%Y%m%d-%H%M%S)"
fi

# Resolve tool-specific settings
case "$AI_TOOL" in
  claude)
    IMAGE_NAME="pact-claude-agent"
    DOCKERFILE="$SCRIPT_DIR/Dockerfile.claude"
    ;;
  gemini)
    IMAGE_NAME="pact-gemini-agent"
    DOCKERFILE="$SCRIPT_DIR/Dockerfile.gemini"
    ;;
  *)
    echo "Error: Unknown tool '$AI_TOOL'. Supported: claude, gemini"
    exit 1
    ;;
esac

if [[ ! -f "$DOCKERFILE" ]]; then
  echo "Error: Dockerfile not found: $DOCKERFILE"
  exit 1
fi

# Create session directories on the host
SESSION_DIR="$SESSIONS_DIR/$SESSION_NAME"
mkdir -p "$SESSION_DIR/workspaces"
mkdir -p "$SESSION_DIR/tool-state"

# Build image
echo "==> Building image ($AI_TOOL)..."
docker build -q -t "$IMAGE_NAME" -f "$DOCKERFILE" "$SCRIPT_DIR" > /dev/null

echo "==> Session: $SESSION_NAME"
echo "==> Tool: $AI_TOOL"
echo "==> Host path: $SESSION_DIR"

# Build tool-specific docker run args
TOOL_ARGS=()

case "$AI_TOOL" in
  claude)
    TOOL_ARGS+=(
      -v "$SESSION_DIR/tool-state:/home/aztec-dev/.claude"
      -v "$HOME/.claude/.credentials.json:/opt/claude-credentials.json:ro"
    )
    ;;
  gemini)
    TOOL_ARGS+=(
      -e "GEMINI_API_KEY=${GEMINI_API_KEY:-}"
    )
    ;;
esac

# Entrypoint starts as root, aligns aztec-dev UID to match host, then drops
# privileges via gosu. No root process remains after that.
exec docker run \
  --rm \
  --name "${IMAGE_NAME}-${SESSION_NAME}" \
  -v "$SESSION_DIR/workspaces:/workspaces" \
  -e "HOST_UID=$(id -u)" \
  -e "HOST_GID=$(id -g)" \
  -e "CI=0" \
  "${TOOL_ARGS[@]}" \
  -it \
  "$IMAGE_NAME" \
  "${AGENT_ARGS[@]}"
