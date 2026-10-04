# Sourced by the entrypoint as the agent user, after the Nix tools are available.
# These settings apply to every Git process launched by the agent, including
# in repositories with an existing local identity or signing configuration.
pact_git_settings=()
if [[ -n "${PACT_GIT_NAME:-}" ]]; then
  pact_git_settings+=("user.name=$PACT_GIT_NAME")
fi
if [[ -n "${PACT_GIT_EMAIL:-}" ]]; then
  pact_git_settings+=("user.email=$PACT_GIT_EMAIL")
fi
if [[ -n "${PACT_GIT_SSH_KEY:-}" ]]; then
  install -d -m 0700 "$HOME/.ssh"
  install -m 0600 "$PACT_GIT_SSH_KEY" "$HOME/.ssh/pact_identity"
  pact_git_settings+=(
    "user.signingkey=$HOME/.ssh/pact_identity"
    "gpg.format=ssh"
    "commit.gpgsign=true"
    'core.sshCommand=ssh -i "$HOME/.ssh/pact_identity" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new'
  )
fi
export GIT_CONFIG_COUNT="${#pact_git_settings[@]}"
for i in "${!pact_git_settings[@]}"; do
  export "GIT_CONFIG_KEY_$i=${pact_git_settings[$i]%%=*}"
  export "GIT_CONFIG_VALUE_$i=${pact_git_settings[$i]#*=}"
done
unset pact_git_settings i
