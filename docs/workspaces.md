# Managed workspaces

Plain `pact` and `pact -p 'Investigate deployment errors'` allocate a new empty
directory under the configured `workspaceRoot`, defaulting to
`~/.local/state/pact/workspaces` (`$XDG_STATE_HOME/pact/workspaces` when set). Each launch
gets its own directory and private Codex state volume. The foreground launcher prints the session ID; use
`pact sessions show ID` to find its workspace and `pact resume ID` to return to it.

For a fresh managed session, only its directory is mounted as
`/home/pact/workspace`. The catalogue, sibling workspaces, and host credentials
stay outside that mount. Agents clone zero or more authorized repositories using
ordinary Git commands inside the container; the host broker supplies transport
and signing without running Git against workspace files. No repository is
required at the workspace root.

`pact -w PATH` uses an existing directory and records it as operator owned.
The operator chooses the entire directory exposed to the agent through this
mount. Pact trusts this choice and does not check for host config, credentials,
or sockets inside an explicitly supplied workspace. Repeated launches there get
independent agent state but share workspace files. A supplied alias of an existing managed directory retains its managed
ownership.

## Layout configuration

`workspaceRoot` and `databasePath` are independent settings in Pact's config.
Both accept absolute paths, `~/` paths, or paths relative to the config directory.
Config loading resolves symlinks and checks that the managed root contains
neither the database nor the Pact config directory or file. New directories and
a database that does not exist yet are supported. These checks concern the
configured root, not individual supplied or saved workspaces.

Changing `workspaceRoot` affects newly allocated workspaces. Existing sessions
resume at their recorded paths; Pact does not move them. Restart the daemon to
apply a new root to `pact start` sessions.

The previous defaults were `~/.local/state/pact/sessions.db` and
`~/.local/state/pact/workspaces`. To keep using them, set:

```json
{
  "databasePath": "~/.local/state/pact/sessions.db",
  "workspaceRoot": "~/.local/state/pact/workspaces"
}
```

## Persistence and recovery

Exiting or failing to start a container retains the directory and catalogue
records. Resume reuses untracked files, local commits, unfinished merges, and
build output alongside the private agent state. It does not retain running
processes or a previous Docker-in-Docker daemon's contents.

Pact records allocation intent, session storage and execution before
creating a managed directory. After creation it marks the workspace ready.
Allocation errors name the session and
path and record a startup failure. An abrupt launcher exit may leave an
`allocating` workspace and a `starting` execution; inspect the saved session
before recovery. Pact never removes a partial allocation automatically.

A missing directory or redirected path blocks resume and new launches against
that registration. Restore the workspace files at the recorded location.
Pact does not track directory device or inode numbers, so restoring files from
a backup does not require preserving the original directory identity.
An incomplete allocation needs explicit operator investigation. Keep
the catalogue, directories, and private state volumes together in backups.
The runtime uses a bind mount that refuses missing sources rather than creating
an empty host directory. Do not move or replace a directory while a session is
running.

Pact does not lock workspaces, private state, provisioning, or reclamation.
Avoid concurrent launches or maintenance against the same resources. After a
launcher crash, manually stop/remove previous containers before resuming or
removing their workspace; Pact does not recover them.
