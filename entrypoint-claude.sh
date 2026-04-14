#!/usr/bin/env bash
source /usr/local/bin/entrypoint-common.sh

align_uid "$@"

# --- Claude-specific setup ---
ln -sf /opt/claude-credentials.json /home/aztec-dev/.claude/.credentials.json

if [[ ! -f /home/aztec-dev/.claude.json ]]; then
  cp /opt/claude-defaults/claude.json /home/aztec-dev/.claude.json
fi

if [[ ! -f /home/aztec-dev/.claude/settings.json ]]; then
  mkdir -p /home/aztec-dev/.claude
  cp /opt/claude-defaults/settings.json /home/aztec-dev/.claude/settings.json
  echo "==> Initialized Claude settings from defaults."
fi

# --- Parse args ---
usage() {
  cat <<'USAGE'
Usage: [options] <task description>

Options:
  --branch <name>       Working branch name (default: agent/<timestamp>)
  --base <branch>       Base branch to start from (default: next)
  --prompt-file <path>  Read task from a file instead of CLI arg
  --continue            Resume the most recent Claude session
  --resume <id>         Resume a specific Claude session by ID
  --shell               Drop into a shell
USAGE
}

parse_args "$@"

if $SHELL_MODE; then
  exec /bin/bash
fi

# --- Resume mode ---
if [[ "$RESUME_MODE" == "continue" ]]; then
  cd "$REPO_DIR"
  echo "==> Resuming most recent Claude session..."
  echo "==> Branch: $(git branch --show-current)"
  exec claude --continue --dangerously-skip-permissions
fi

if [[ "$RESUME_MODE" == "resume" ]]; then
  cd "$REPO_DIR"
  echo "==> Resuming Claude session: $SESSION_ID"
  echo "==> Branch: $(git branch --show-current)"
  exec claude --resume "$SESSION_ID" --dangerously-skip-permissions
fi

# --- New task ---
if [[ -z "$PROMPT" ]]; then
  echo "Error: No task provided."
  usage
  exit 1
fi

setup_repo
echo "==> Task: $PROMPT"
echo ""

claude -p "$PROMPT" --dangerously-skip-permissions

cleanup_repo
