# Sourced by the entrypoint as the agent user, after the Nix tools are available.
# These settings apply to every Git process launched by the agent, including
# in repositories with an existing local identity or signing configuration.
# The image installs cmd/git-client as /usr/local/bin/pact-git-sign and symlinks
# pact-git-ssh to it. The executable name selects signing or SSH transport.
pact_git_settings=()
install -d -m 0700 "$HOME/.config/pact"
# A command-scope include preserves precedence over repository settings while
# allowing pact-git-sign --refresh to update future Git commands in this shell.
pact_git_identity="$HOME/.config/pact/git-identity.config"
if [[ -n "${PACT_GIT_NAME:-}" ]]; then
  git config --file "$pact_git_identity" user.name "$PACT_GIT_NAME"
fi
if [[ -n "${PACT_GIT_EMAIL:-}" ]]; then
  git config --file "$pact_git_identity" user.email "$PACT_GIT_EMAIL"
fi
pact_git_settings+=("include.path=$pact_git_identity")
if [[ -n "${PACT_GIT_URL:-}" ]]; then
  # Public verification files are populated lazily by the signing helper.
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
unset pact_git_settings pact_git_identity i
