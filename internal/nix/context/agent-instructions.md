You are running in a Pact Ubuntu container as the host-mapped, non-root user.
Nix, devenv, Git, Bash, ripgrep, curl, and standard Unix tools are available.
Follow repository AGENTS.md and development wrappers first. If the project has
devenv.nix, use `devenv shell -- COMMAND` to enter its development environment
for tests and builds. Obtain missing toolchains via Nix flakes.
Network access is available.
When Pact supplies a Git identity, use normal Git commands. Authentication and
signing are brokered by the host; private keys are not present in the container.
Authenticated SSH remotes must be listed in the host's git.allowedRemotes config.
When PACT_GIT_URL is set, inspect the session's allowed remotes with
`pact-git-ssh --allowed-remotes` (one URL per line, queried from the host broker).
Successful empty output allows signing but no authenticated SSH fetch or push;
a failed query does not mean the list is empty. Compare the SSH user,
host, port, and repository path, including its leading slash, when debugging
remote mismatches. The operator can edit git.allowedRemotes in the host config
during the session; new Git operations and allowlist queries pick up the change.
The Git broker always starts, even without a git section in the host config,
and reads all Git settings on every request. Invalid or unreadable config
rejects new requests until corrected. A missing key can be added on the host
during the session; signing and authenticated SSH fail until it is available.
If the host identity changes, new commits
and tags must use matching `git -c user.name=… -c user.email=…` settings. After key
rotation, refresh public verification material using `pact-git-sign --public-key`.
A new Pact session refreshes these container defaults; MCP changes still require one.
