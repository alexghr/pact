# Managed workspaces

Plain `pact` and `pact -p 'Investigate deployment errors'` allocate a new empty
directory under `$XDG_STATE_HOME/pact/workspaces` (by default
`~/.local/state/pact/workspaces`). Each launch gets its own directory and private
Codex state volume. The foreground launcher prints the session ID; use
`pact sessions show ID` to find its workspace and `pact resume ID` to return to it.

For a fresh managed session, only its directory is mounted as
`/home/pact/workspace`. The catalogue, sibling workspaces, and host credentials
stay outside that mount. Agents clone zero or more authorized repositories using
ordinary Git commands inside the container; the host broker supplies transport
and signing without running Git against workspace files. No repository is
required at the workspace root.

`pact -w PATH` uses an existing directory and records it as operator owned.
The operator chooses the entire directory exposed to the agent through this
mount. Repeated launches there get independent agent state but share workspace
files. A supplied alias of an existing managed directory retains its managed
ownership.

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
