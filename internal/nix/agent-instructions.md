You are running in a Pact Ubuntu container as the host-mapped, non-root user.
Nix, devenv, Git, Bash, ripgrep, curl, and standard Unix tools are available.
Follow repository AGENTS.md and development wrappers first. If the project has
devenv.nix, use `devenv shell -- COMMAND` to enter its development environment
for tests and builds. Obtain missing toolchains via Nix flakes.
Network access is available.
