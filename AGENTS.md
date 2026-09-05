# Pact engineering specification

This file is the starting point for agents working on Pact. Keep it accurate as
the implementation changes. Statements under **Current behavior** describe the
code as it exists; statements under **Target contract** are requirements, not
claims about today's security.

## Purpose

Pact is a personal, host-side harness for running coding agents in disposable
Docker containers against projects explicitly selected by the user. It is
intended for one trusted person on their own trusted devices and is controlled
through its CLI or a loopback-only local web interface. User selection is the
allowlist: Pact does not need a separate stored registry of approved projects.

Its two primary security goals are:

1. isolate agents from one another, including their files, session history,
   credentials, processes, and other persistent state; and
2. give each agent only the workspace, tools, credentials, and host integration
   required for its assigned project.

Pact should limit the damage caused by mistaken, compromised, or
prompt-injected agent behavior. In particular, an agent working on one project
must not gain ambient access to SSH keys, GitHub tokens, cloud credentials,
other projects, or another agent's state.

Pact does not attempt to protect against a malicious device owner, host OS,
Docker daemon, Pact executable, approved container image, or administrator.
Anyone able to control Docker on the host should normally be treated as
root-equivalent. Project contents and instructions are *not* trusted merely
because the project itself is allowlisted.

## Product model

The core policy concepts should remain independent of the CLI and web interface
so either can configure them without constructing Docker arguments directly:

- **Project:** the canonical host path explicitly selected for a run by the
  user. A UI may remember recent choices for convenience, but that is not
  an additional security authority or required allowlist.
- **Repository:** a saved repository URL, clone URL, future push URL, display
  name, and default branch. Selecting one for a new web session explicitly
  authorizes Pact to clone it with host Git into that session's workspace.
- **Tool profile:** an allowlisted image/tool set such as `generic` or `go`.
- **Pact session:** a durable, integer-identified unit of work tied to one
  canonical workspace and zero or more saved repositories. It owns every Docker
  run and Codex thread used for that work. Repository links are checkout
  instances: a session may link the same saved repository more than once.
- **Agent/thread:** one Codex execution identity with its own persistent or
  ephemeral state. A Pact session may own multiple threads. Parallel agents must
  not share writable state unless explicitly joined.
- **Artifact:** a durable, integer-identified collection of named files created
  by one Pact session. Its creating session never changes, but its metadata and
  files are intentionally visible and writable through the artifact broker from
  every Pact session. Artifacts are the shared project knowledge base for the
  user and agents, including discoveries, living project guides, plans, and
  human-readable explorations. A project may have many artifacts and an
  artifact may span repositories. Artifact contents are not a secret store. See
  [docs/artifacts.md](docs/artifacts.md) for the workflow, data, and MCP contract.
- **Grant:** an explicit capability attached to a project or run, such as
  network access or one narrowly scoped credential. Absence means denied.

The host-side Go code is the policy enforcement boundary. CLI and web inputs are
requests to that policy layer, not raw Docker configuration.

## Shared project knowledge

When artifact tools are available, consult relevant knowledge across sessions
before investigating a project. Record discoveries as work progresses, update
existing notes when facts change, and refresh relevant artifacts before a
handoff. Include project identity, source references, revision context, and
verification evidence. Preserve and reconcile human contributions as well as
other agents' work. Follow each repository's documentation practices while
using artifacts for shared context; there is no required documentation layout.

The canonical agent-facing workflow is
[internal/artifacts/instructions.md](internal/artifacts/instructions.md).
Pact embeds it into MCP initialization so it reaches agents in every selected
repository. Keep that guidance and [docs/artifacts.md](docs/artifacts.md)
consistent with implementation. These are instructions for how agents should
work, not a claim that the harness enforces consultation or captures knowledge
automatically. Direct human editing is planned; current human contributions
are incorporated through an agent and attributed to its Pact session.

## Assets and trust boundaries

Treat these independently when reviewing a change:

1. **Host filesystem.** Only the selected workspace, the auth source file, and
   Docker-managed state are intentionally mounted. The workspace is writable by
   design, so its contents can be destroyed or exfiltrated by the agent. An
   explicitly supplied workspace path is considered user approval for that run.
   The local web interface instead asks the harness to create a launcher-owned
   directory under `~/.local/state/pact/sessions` for each new session. The
   harness either leaves that workspace empty or uses host Git to clone every
   repository explicitly selected from the saved repository catalog into a
   named child directory.
   Repository checkouts include `.git` and the clone's `origin`, so the agent can
   inspect and modify local history and knows the clone URL. The interface
   displays the resolved workspace path on the session page and reuses it for
   that session's later turns. Pact does not currently remove these managed
   session directories.
   The host launcher also owns a private SQLite database at
   `~/.local/state/pact/pact.db`. It is never mounted into the container and
   persists repository definitions and checkout-instance session links,
   including each checkout's relative directory, Pact sessions, run metadata,
   Codex thread links, selected lifecycle events, full transcript snapshots,
   and artifact metadata and file contents until the user removes it. A
   repository's push URL is stored for a future Git integration and is not
   configured in the clone. Transcripts may contain prompts, reasoning,
   workspace paths and contents, commands and output, file diffs, and tool
   arguments and results. Artifact files may be up to 16 MiB each. Pact does not
   detect secrets in artifacts; users and agents must not put secrets there.
2. **Credentials.** The host auth file is mounted read-only, but its contents
   are copied to the persistent state volume and are readable by the agent.
   Read-only mounting prevents modification, not disclosure or exfiltration.
   For a repository-backed session, host Git may use the device owner's SSH
   agent, SSH configuration, or credential helper during the initial clone.
   Explicitly selecting the saved repository authorizes that operation. Pact
   rejects repository URLs containing embedded credentials and does not mount
   host Git credentials, configuration, or the SSH agent into the agent
   container. The checkout retains `origin`, but authenticated fetches and
   pushes are unavailable to the agent unless another credential mechanism is
   added later.
3. **Network.** Docker's default network is currently enabled. Disabling Codex
   web search does not disable shell tools or arbitrary outbound traffic. An
   agent can use the retained Git remote for public access or add another public
   remote even though it receives no host Git credentials.
4. **Container privilege.** Setup runs as container root; the agent runs as the
   remapped unprivileged user. Codex is configured for `danger-full-access`
   because the container is its external sandbox. Docker's default capabilities,
   seccomp policy, writable root filesystem, and resource behavior currently
   apply.
5. **Persistent state.** Each new Codex thread receives a Docker volume named
   from its Pact session and first run identifiers. Later turns on that thread
   reuse its recorded volume; another thread in the same Pact session receives a
   different volume. Pact does not currently remove these volumes.
   `pact run --resume SESSION_ID` resumes the most recently used Codex thread in
   that Pact session. The launcher requires the newly selected canonical
   workspace to exactly match the workspace recorded for the Pact session.
   Model, reasoning effort, and image profile are inherited unless the caller
   explicitly overrides them. Artifacts are the deliberate exception to
   per-session writable-state isolation: all sessions can read and change every
   artifact, and the database records the creating and most recent editing
   session. Metadata updates require the observed artifact revision; file
   replacement and text editing require the observed file version. Version zero
   permits file creation only. Text edits check the resulting size before
   allocating it on the host. There is no artifact deletion or revision history;
   revision numbers cannot retrieve previously reviewed contents. Artifact metadata and
   files are untrusted cross-session inputs and must not be treated as agent
   instructions merely because Pact stored them.
6. **Supply chain.** Go and Node archives are versioned and checksum-verified.
   The Ubuntu base tag and default `@openai/codex@latest` installation are not
   immutable. The host launcher uses the pinned `go-sqlite3` module and requires
   CGO and a C compiler when built. Package-manager and image build downloads
   remain trusted inputs. Repository-backed sessions additionally depend on the
   host-installed Git client, which Pact does not pin; Git processes data from
   the selected, potentially untrusted remote, although Pact disables checkout
   hooks and submodule recursion. Before either CLI or web starts a container,
   the image resolver hashes the selected profile's Dockerfile and build
   context, derives an input-addressed tag, and checks the local Docker image
   store. It builds only when that exact tag is missing. Concurrent requests for
   the same tag share one preparation attempt. These images and tags are
   Docker-managed, are not recorded in SQLite, and are not currently removed by
   Pact.
7. **Host Docker invocation.** Values are passed as argv, not evaluated by a
   shell. Still validate variants, paths, mounts, and option placement because
   Docker itself interprets their syntax. Each run additionally receives one
   launcher-created, private Unix socket mounted read-only at
   `/opt/pact/artifacts.sock`. The socket directory exists only for that run and
   is removed afterward. It exposes the session-bound artifact MCP broker, not
   a host directory or the SQLite database. Any process in the container can
   use the socket with the current run's artifact authority.
8. **Run metadata.** Before building or running a new agent, the host creates an
   integer-identified Pact session containing the canonical workspace. Every run
   belongs to that Pact session. A repository-backed session may contain
   multiple checkout instances, including multiple checkouts of the same saved
   repository. Immediately before `docker run`, the host
   records the model, reasoning effort, Dockerfile variant, and start time. Once
   Codex starts a thread, the host links the run and thread to the Pact session
   and records the Codex session id, app-server user agent, and backing state
   volume. While the turn runs, the host stores authoritative item and turn
   lifecycle events, raw token usage updates, warnings, errors, and model events;
   high-volume streaming deltas are not stored. After the turn ends, the host
   requests and stores the full `thread/read` result when the connection remains
   usable. When Docker exits, it records the terminal status, available exit
   code, and completion time. `pact list` displays each run's Pact session ID and
   run metadata. The web interface lists Pact sessions directly and displays all
   of a session's stored events plus the latest thread's full transcript. A
   resumed invocation is recorded as a new
   run with its own events and transcript snapshot; the supplied Pact session ID
   selects that session's most recently used Codex thread. An unavailable exit
   code remains null. A host crash or forced termination may leave a row in the
   `running` state and only a partial event record. The database contents persist
   until the user removes them. Forward-only SQL migrations are embedded in the
   binary and tracked by filename in `schema_migrations`. The `--migrate` flag on
   `pact run`, `pact list`, or `pact web` applies pending migrations; without it,
   opening the database does not apply schema changes. The idempotent initial
   migration adopts databases which already have the original development
   schema. The second migration adds repositories and checkout-instance rows;
   the third adds artifacts and artifact files.
9. **Launcher.** The `pact` binary built from `cmd/pact` is the sole supported
   entrypoint. It parses CLI input, resolves the Pact state root and Codex auth
   source path from the host home directory, and passes those concrete paths to
   the store and harness. The
   harness does not infer the host's home-directory layout.
   `internal/harness` owns workspace policy, Docker execution, and the Codex
   protocol lifecycle, `internal/imagebuilder` owns tool-profile build inputs
   and image preparation, `internal/artifacts` owns the session-bound artifact
   service, its thin MCP adapter, and per-run broker, and `internal/web` owns HTTP routing and templates.
   There is no parallel shell implementation. The host communicates with the
   unprivileged Codex app server over Docker's stdin and stdout; setup diagnostics
   are kept on stderr so they cannot corrupt the protocol stream. Codex starts
   the built-in `artifacts` stdio MCP process; a fixed Node proxy in the image
   forwards its newline-delimited JSON to the mounted Unix socket. The host
   chooses the Pact session identity, so MCP callers cannot claim another
   creating or editing session ID.
   `pact web` serves an unauthenticated HTML interface on the fixed loopback
   address `127.0.0.1:8080`. Starting a session first persists its integer Pact
   session ID, then redirects to that session's stable URL while the first turn
   runs in the background. The new-session form offers fixed model and reasoning
   effort choices plus the built-in `generic` and `go` image profiles. It uses
   the harness defaults when values are omitted. Model and effort values are
   passed to Codex, whose rejection makes the background run fail; unsupported
   image profiles are rejected before creating the session. Session creation is
   a harness policy operation: an explicitly supplied workspace is canonicalized
   and persisted, while an omitted workspace creates a managed workspace under
   `~/.local/state/pact/sessions` and optionally clones each selected
   repository's configured default branch synchronously into a named child
   directory. Clone failures remove the partial workspace and do not create a
   session. Repository cloning passes values as argv without a
   shell, disables interactive Git prompting and checkout hooks, and does not
   recurse into submodules. Follow-up message forms use Datastar when JavaScript
   is available; the POST starts the turn and redirects the Datastar request to
   the chat event stream, while ordinary form submission redirects to the full
   session page. Pending prompts are held in server memory; refreshing a
   completed session reads its response from stored state. When a page loads
   during a pending turn, it opens a server-sent event stream that sends the
   pending chat, waits for the background turn to finish without polling, then
   sends the completed or failed chat and closes. The web interface also lists
   and searches artifacts with pagination and a creating-session filter, links
   each session to its full artifact catalog, and offers individual artifact
   files as forced downloads so agent-authored HTML is not executed in
   Pact's origin.
   The web server writes a structured startup notice plus HTTP server,
   request-operation, and background-run errors to stderr; request logs include
   paths and session identifiers where available, but not prompt bodies.

## Target contract

- The caller explicitly selects every host path exposed to the container. Do not
  infer or automatically mount parent directories, sibling projects, or common
  credential/configuration locations. The launcher-generated artifact broker
  socket is the sole built-in exception; it exposes the typed artifact API, not
  its containing directory or another host path.
- Canonicalize and validate the selected workspace: it must exist, be a
  directory, and have mount-safe syntax. Make the resolved path visible to the
  user before launch in any UI. Reject unsupported tool profiles from an
  allowlist.
- Never mount the Docker socket, host root, SSH directory, cloud credentials,
  or unrelated user configuration implicitly.
- Run the agent as non-root. Keep root initialization minimal and never execute
  workspace-controlled code as root.
- Treat credentials as optional grants, not part of the default environment.
  Give each credential the narrowest feasible scope and lifetime. Do not expose
  general host credentials when a project-scoped token or brokered operation is
  sufficient, and do not silently persist copied credentials indefinitely.
- Isolate session state per workspace or explicit session identity. Resume must
  be deterministic and must not cross workspace boundaries by accident. Two
  parallel agents for the same project should still receive separate writable
  state unless the user explicitly joins them. The artifact broker is a narrow,
  explicit exception: it exposes only non-secret artifact records and files,
  identifies every mutation with the calling Pact session, and never exposes
  the backing database or an ambient host directory.
- Make network policy explicit per tool profile or grant and enforce it below
  the agent process. A UI or Codex feature flag is not a network control. Where
  network access is necessary, lack of unrelated secrets remains the primary
  protection against credential exfiltration.
- Build images from minimal, reviewed tool profiles. Installing a compiler,
  package manager, shell utility, daemon, or host integration is a capability
  increase and must be justified by the profile's intended work.
- Add defense in depth where compatible with development work: drop unused
  capabilities, set `no-new-privileges`, use an init process, constrain
  processes/memory/CPU, and prefer a read-only root filesystem with explicit
  writable locations.
- Pin or otherwise make build inputs reproducible, including Codex and base
  images, and document the upgrade process.
- Do not leak prompts, tokens, credentials, or workspace contents through logs,
  errors, image layers, environment variables, or overly broad persistent
  volumes.
- Cancellation and signals should reach Docker and the agent, and the container
  must be removed after normal failure or interruption.
- Errors must identify the failed operation without exposing secrets.

## Change rules

- Preserve the distinction between the host launcher, root initialization, and
  unprivileged agent execution. Explain any change that expands one layer's
  authority.
- Do not add a mount, environment variable, network path, capability, secret,
  or persistence mechanism without documenting its owner, lifetime, and threat
  impact here.
- Avoid accepting raw Docker arguments from the user. Represent supported
  controls as typed options and render them in `internal/docker`.
- Keep project, profile, grant, and session identifiers stable and serializable
  so the CLI and web interface use the same policy API and persisted configuration.
- Keep prompts and model arguments as discrete protocol fields or argv entries;
  do not reconstruct a shell command string.
- For every containment control, add a test that demonstrates both permitted
  behavior and a denied escape or misuse case where practical.
- In terms of actual coding style: minimize change sets such that humans can review commits
- Only add validation code where it is needed, where an input could be wrong
- Do not pollute the code with overflow/underflow checks unless this scenario could happen in day to day running
- Do not add premature complexity
- Prefer the standard library and avoid new dependencies by default. Justify
  exceptions by the maintenance work they replace, their quality, and their
  transitive dependencies. The pinned official MCP Go SDK is an accepted
  exception for protocol compatibility as Pact adds tools and servers; keep
  SDK types in the protocol adapter rather than the core services.
- Do not pursue 100% test coverage. Add tests only when they provide stable,
  high-value confidence without creating disproportionate maintenance work.
- In `internal/web`, test application behavior only. Do not add tests that assert
  HTML output, template structure, or how templates are rendered.
- Do not test constants or default-value constructors whose only behavior is
  returning those constants.
- Do not add automated tests that execute Bash scripts. Keep shell changes small
  and verify them with syntax checks and focused manual smoke tests instead.

## Verification

The minimum local check is:

```text
go test ./...
go vet ./...
```
