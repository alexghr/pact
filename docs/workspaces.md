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

Pact does not lock workspaces, private state, provisioning, or reclamation.
Avoid concurrent launches or maintenance against the same resources. Recorded
Docker resources still need reconciliation before a replacement starts.

## Explicit workspace removal

`pact workspaces remove ID` previews the path and affected session IDs. To
permanently delete that managed directory and all of its contents, run
`pact workspaces remove ID --yes`. This includes untracked files, local commits,
repositories, and build output. Review or copy anything you need first.
There is no undo. Stopping a container, finishing a session, or archiving
history does not authorize deletion.

The operation retains the catalogue, native conversation volumes, private Nix
roots, and shared Nix storage. Other workspaces remain intact, including paths
reached by symlinks inside the selected directory. Retained artifacts and memory
must also live outside this deletable directory when those features arrive.
This command is separate from `pact environments gc`, which only reclaims
unreferenced private environment resources.

Only a completed managed allocation owned by the current operator is eligible.
Pact checks the directory identity and location and refuses supplied paths,
redirected/replaced directories, and overlapping workspace registrations. All
sessions and environments referring to the directory participate in the same
writer and recovery checks, including environments without a retained session.
A surviving recorded container or uncertain Docker inspection blocks removal.
Stop the reported writer explicitly and retry; cleanup never stops a surviving writer on your
behalf.

Removal does not serialize recovery and deletion against launches. Files are
removed through an open directory root so workspace symlinks cannot redirect
deletion outside it. Catalogue intent becomes
`cleanupPending` before removing files. Interrupted or failed deletion can leave
only some files behind; inspection reports this status and all launches/resumes
against the directory are blocked. Correct the reported filesystem or Docker
problem and retry the same `remove ID --yes` command. If deletion finished before
the launcher could record completion, retry records the missing directory as
intentionally removed.

Completed removal records `removed`, preserving the original path and history.
Repeating removal is harmless while that path stays absent. A new directory at
the old path is refused, and resume explains the intentional deletion instead of
allocating a replacement. Start a new session for new work. Do not manually move
or replace a workspace while Pact owns it.
