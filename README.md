# Pact

Pact is an agent isolation solution using your existing ChatGPT subscription. 

## Running sessions

Build with `./shell p-build`, then run `./pact daemon` in a separate terminal
(or as a background process). In another terminal:

```sh
./pact                      # Create and attach to an interactive session
# Enter a prompt, then press Ctrl-] to detach while it works.
./pact attach 42            # Reattach using the printed Pact session ID
./pact -d                   # Create a session without attaching yet (--detach)
```

`pact start` is an alias for starting a new session. Both accept workspace, model,
effort, config, DinD and host-network flags. Enter prompts in the native TUI;
`--prompt` and `-p` are unavailable.
Ctrl-C reaches Codex. Ctrl-] only disconnects the terminal.

The daemon keeps live sessions in memory and replays up to 1 MiB of raw
terminal output on attachment. Screen restoration, especially after a resize,
is experimental. One terminal can attach to each session. Build and startup
diagnostics appear in the daemon terminal. Stopping the daemon stops its agent
containers; workspace files and saved conversations remain. There is no crash
recovery. All session execution runs through the daemon.

## Sessions

Run `pact` for an interactive session in a fresh managed workspace. Use
`pact -w PATH` to supply an existing directory. Pact retains workspace files,
private agent state, and launch settings between runs. Pact uses Nix shells
behind the scense to give agents access to all the tools they need to achieve
their tasks. The Nix store is shared between all Pact sessions so that storage
space is used efficiently.

Each new session prints its Pact ID. Find sessions from any directory with
`pact sessions list`, or inspect one with `pact sessions show 42`. Both accept
`--json`; listing supports `--workspace PATH`.
The catalogue defaults to `~/.local/state/pact/store.sqlite`; managed workspaces
default to `~/.local/state/pact/workspaces/`. `XDG_STATE_HOME` overrides
`~/.local/state`.
Set `databasePath` and `workspaceRoot` in `~/.config/pact/config.json`
(or the file selected by `pact --config PATH daemon`) to choose other locations:

```json
{
  "databasePath": "~/.local/state/pact/store.sqlite",
  "workspaceRoot": "~/.local/state/pact/workspaces"
}
```

Relative paths resolve against the config directory. The managed workspace root
must not contain the database or Pact config. For `-w PATH`, you choose everything
exposed to the agent; Pact does not check for host config or credential files
inside that directory.

The daemon selects storage locations at startup. Start clients with the same
config, or another config with matching `databasePath` and `workspaceRoot`.
If those paths differ, restart the daemon with the intended config.

Existing catalogues are not moved automatically. To keep an older installation,
set `databasePath` to its existing `sessions.db` and `workspaceRoot` to its existing
workspace directory. Saved sessions retain their recorded workspace locations.

A Pact session binds once to the agent's initial native conversation. Run
`pact resume` to resume the Pact session with the most recently started execution
from any directory, or `pact resume 42` to select a session. If it is still running, resume reattaches
to its native TUI. Otherwise, it starts a new execution using its saved model,
reasoning effort, Docker-in-Docker and host-network capabilities, workspace, and
private state. New executions use current host configuration for Git and MCP;
`--config PATH` selects another config file with matching storage locations.
Live reattachment keeps the execution's existing configuration. Use `pact resume 42 -d` to resume
without attaching, then enter follow-up prompts after attaching.

Codex is currently the supported backend. Native `/new` and `/resume` navigation
does not change the Pact session's saved backend reference. Pact does not list
native threads or select the most recently used native conversation. A session
with no backend reference starts its first conversation on its next run. An
unavailable saved conversation causes a native resume error.

The daemon records execution outcomes and removes temporary agent/DinD
containers and socket volumes on exit or handled cancellation. Shared Nix
storage and private session state remain. A hard crash can leave running
containers and unfinished records: stop/remove stray resources manually with
`docker rm -f` and `docker volume rm` before resuming. Pact does not lock writers
or recover previous executions; interrupted prompts are not replayed.

## System skills

The container includes [Graphene](https://github.com/alexghr/graphene) and its
`graphene-stacked-prs` skill. Nix pins the CLI and skill to the same source revision
in `internal/images/context/agent/env/flake.lock`.

Bundled skills are listed in `internal/images/context/agent/env/flake.nix` and exposed through
`/etc/codex/skills`. To add or remove one, edit the `skills` list (and add any
required tools to `pact-tools`), then rebuild the launcher with `./shell p-build`.
Each agent starts in a Nix shell using its pinned toolset. Sessions share cached
package outputs, while each container keeps its own tools and skills. Tool updates
apply to newly started containers and do not change the daemon image.

Project skills in `.agents/skills` inside the Git clone remain available through
Codex's normal skill discovery.

To review a PR, start `pact` and enter
`Use $review-pr to review https://forge.example.com/team/project/pulls/123`.
The bundled review skills use the target repository's review policy. Include
“publish findings” to post a review through your configured forge connection.
See [code review](docs/code-review.md) for policy setup and review behavior.

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
[the MCP bridge documentation](docs/mcp-bridge.md) for endpoints, credentials, and
tool permission regexes. Use `allowedTools` for automatic allowance and
`approvalTools` for per-call decisions from an external service over a host Unix
socket. Other tool calls are denied.

See [the approval protocol](docs/approval-protocol.md) for setup.
