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
mount. Repeated launches there get independent agent state but share workspace files
and therefore the same execution lock. A supplied alias of an existing managed
directory retains its managed ownership.

## Persistence and recovery

Exiting or failing to start a container retains the directory and catalogue
records. Resume reuses untracked files, local commits, unfinished merges, and
build output alongside the saved agent environment. It does not retain running
processes or a previous Docker-in-Docker daemon's contents.

Pact records allocation intent, session, environment, and execution before
creating a managed directory. After creation it records the directory's device
and inode identity and marks it ready. Allocation errors name the session and
path and record a startup failure. An abrupt launcher exit may leave an
`allocating` workspace and a `starting` execution; inspect the saved session
before recovery. Pact never removes a partial allocation automatically.

A missing directory, redirected path, or replaced managed directory blocks
resume and new launches against that registration. Restore the original
directory at its recorded location; creating an empty replacement is not
recovery. An incomplete allocation needs explicit operator investigation. Keep
the catalogue, directories, and private state volumes together in backups.
The runtime uses a bind mount that refuses missing sources rather than creating
an empty host directory. A directory must not be moved or replaced while Pact
owns it.

New launches, provisioning, and reclamation share the catalogue storage lock.
Executions additionally hold workspace and private-state locks through runtime
cleanup. A crashed launcher releases its OS locks; recorded Docker resources
still need reconciliation before another writer can start. Workspace allocation
does not change the existing execution and environment recovery rules.
