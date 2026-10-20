# Pact

Pact is an agent isolation solution using your existing ChatGPT subscription. 

## Sessions

Each validated launch prints a Pact session ID to stderr and records the workspace,
launch settings, and outcome on the host, including when startup fails. The
catalogue lives at `$XDG_STATE_HOME/pact/sessions.db`, defaulting to
`~/.local/state/pact/sessions.db`. Each new session has private persistent agent
state; sessions share a managed Nix daemon and read-only package store.

Find saved sessions with `pact sessions list` in the workspace directory, or
`pact sessions list --all` across workspaces. Use `pact sessions show 42` to
inspect a session and `pact sessions threads 42` to list its native thread IDs.
Each command accepts `--json`. Listings show saved metadata, updated during
interactive use and after execution. Resume refreshes it automatically before
choosing a conversation.

Resume the last used saved conversation with `pact resume 42`, or select one
explicitly with `pact resume 42:THREAD_ID`. Pact reuses the
session's workspace, private state, and recorded launch settings, and opens the
native Codex terminal UI. Git and MCP connections use the current host config;
pass `--config PATH` to select another config.

Pact selects the conversation with the newest saved last-use timestamp in Codex.
Opening an older conversation can update this without sending a message;
switching back to an already-loaded thread may leave the timestamp unchanged.
An empty `/new` is
saved only after its first message, so leaving it keeps the previous saved
conversation as the target. Missing, archived, or unreadable saved state produces
an error rather than starting a different conversation.

Coordinate launches manually: stop the previous container before resuming, and
avoid overlapping sessions in the same workspace. Automatic execution ownership
and crash recovery are not implemented yet. History from the older global state
volume is not imported automatically.

See [storage and recovery](docs/nix-storage.md) for persistent Nix roots,
`pact environments repair SESSION_ID`, and `pact environments gc`.

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
