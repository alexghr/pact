# Sourced by the entrypoint as the agent user, after the Nix tools are available.
# These settings apply to every Git process launched by the agent, including
# in repositories with an existing local identity or signing configuration.
# The image installs cmd/git-client as /usr/local/bin/pact-git-sign and symlinks
# pact-git-ssh to it. The executable name selects signing or SSH transport.
pact_git_settings=()
if [[ -n "${PACT_GIT_NAME:-}" ]]; then
  pact_git_settings+=("user.name=$PACT_GIT_NAME")
fi
if [[ -n "${PACT_GIT_EMAIL:-}" ]]; then
  pact_git_settings+=("user.email=$PACT_GIT_EMAIL")
fi
if [[ -n "${PACT_GIT_URL:-}" ]]; then
  # Only public material enters the container. The host keeps the private key.
  install -d -m 0700 "$HOME/.config/pact"
  pact-git-sign --public-key > "$HOME/.config/pact/git-signing.pub"
  printf '%s %s\n' "$PACT_GIT_EMAIL" "$(cat "$HOME/.config/pact/git-signing.pub")" \
    > "$HOME/.config/pact/git-allowed-signers"
  pact_git_settings+=(
    "user.signingkey=$HOME/.config/pact/git-signing.pub"
    "gpg.format=ssh"
    "gpg.ssh.program=pact-git-sign"
    "gpg.ssh.allowedSignersFile=$HOME/.config/pact/git-allowed-signers"
    "commit.gpgsign=true"
    "core.sshCommand=pact-git-ssh"
    "ssh.variant=ssh"
  )
fi
export GIT_CONFIG_COUNT="${#pact_git_settings[@]}"
for i in "${!pact_git_settings[@]}"; do
  export "GIT_CONFIG_KEY_$i=${pact_git_settings[$i]%%=*}"
  export "GIT_CONFIG_VALUE_$i=${pact_git_settings[$i]#*=}"
done
unset pact_git_settings i
