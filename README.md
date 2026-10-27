# Pact

Pact is an agent isolation solution using your existing ChatGPT subscription. 

## Sessions

Run `pact` for an interactive session in a fresh managed workspace, or
`pact -p 'Investigate deployment errors'` to start with a prompt. Use
`pact -w PATH` to supply an existing directory. Pact retains workspace files,
private agent state, launch settings, and Nix roots between runs.

Each new session prints its Pact ID. Find sessions from any directory with
`pact sessions list`, or inspect one with `pact sessions show 42`. Both accept
`--json`; listing supports `--workspace PATH` and `--all`.
The catalogue lives at `$XDG_STATE_HOME/pact/sessions.db`, defaulting to
`~/.local/state/pact/sessions.db`.

A Pact session binds once to the agent's initial native conversation. Resume it
interactively with `pact resume 42`, or continue with a prompt using
`pact resume 42 -p 'Continue the investigation'`. Both reuse its saved model,
reasoning effort, Docker-in-Docker and host-network capabilities, workspace, and
private state. Git and MCP connections use current host configuration;
`--config PATH` selects another config file.

Codex is currently the supported backend. Native `/new` and `/resume` navigation
does not change the Pact session's saved backend reference. Pact does not list
native threads or select whichever was most recently used. A session with no
backend reference starts its first conversation on its next run. An unavailable
saved conversation causes a native resume error.

The launcher records execution outcomes and removes temporary agent/DinD
containers and socket volumes on exit or handled cancellation. Shared Nix
storage and private session state remain. A hard crash can leave running
containers and unfinished records: stop/remove stray resources manually with
`docker rm -f` and `docker volume rm` before resuming. Pact does not lock writers
or recover previous executions; interrupted prompts are not replayed.

See [session management](docs/session-management.md) and
[Nix storage](docs/nix-storage.md) for lifecycle and persistence details.
Preview permanent workspace removal with `pact workspaces remove 42`, then
repeat with `--yes` to delete its files after ownership checks. Native state,
Nix storage, and catalogue history are retained. Stop containers using the
workspace before removing it. See [managed workspaces](docs/workspaces.md).

## System skills

The container includes [Graphene](https://github.com/alexghr/graphene) and its
`graphene-stacked-prs` skill. Nix pins the CLI and skill to the same source revision
in `internal/nix/context/tools/flake.lock`.

Bundled skills are listed in `internal/nix/context/tools/flake.nix` and exposed through
`/etc/codex/skills`. To add or remove one, edit the `skills` list (and add any
required tools to `pact-tools`), then rebuild the launcher with `./shell p-build`.
Each launch asks Nix to realise the pinned toolset, reusing existing build outputs,
then updates the shared tools profile. Running containers see newly added commands
without restarting. Skills are published from that profile into the stable
`/etc/codex/skills` directory so Codex can detect additions and removals.
Already-running programs keep their existing version; upgrading Codex itself
requires restarting its process. Tools and skills do not change the daemon image.

Project skills in `.agents/skills` inside the Git clone remain available through
Codex's normal skill discovery.

## Git

Configure a Git identity in `~/.config/pact/config.json`. Pact keeps the private
SSH key on the host and brokers Git transport and signing; the agent uses normal
`git clone`, `git commit`, `git fetch`, and `git push` commands.

```json
{
  "git": {
    "name": "Pact Agent",
    "email": "agent@example.com",
    "sshKey": "~/.ssh/pact_ed25519",
    "allowedRemotes": ["ssh://forgejo@forge.example.com/team/project"]
  }
}
```

Add the server's verified host key to the host's `~/.ssh/known_hosts` before use.
The Git broker always starts but only works when properly configured. Only the
listed repositories can use the host key. Edit `git.allowedRemotes` on the host
during a session to change access for the next Git operation. The config is
refreshed from disk on every request.

After adding or changing the Git identity, the agent can run
`pact-git-sign --refresh` inside the container, then use Git normally. The command
is safe to repeat and requires no restart.

## MCP

Configure MCP forwarding with named entries under `mcp` in your Pact config. See
[the MCP bridge documentation](docs/mcp-bridge.md) for `url`, `token`, and `tokenFile`.
