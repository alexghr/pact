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
remote mismatches. To change access, the host config must currently be updated
and a new Pact session started.
