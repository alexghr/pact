# Pact engineering specification

This file is the starting point for agents working on Pact. Keep it accurate as
the implementation changes. Statements under **Current behavior** describe the
code as it exists; statements under **Target contract** are requirements, not
claims about today's security.

## Purpose

Pact is a personal, host-side harness for running coding agents in disposable
Docker containers against projects explicitly selected by the user. It is
intended for one trusted person on their own trusted devices, and may later be
controlled by a local UI. User selection is the allowlist: Pact does not need a
separate stored registry of approved projects.

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

The core policy concepts should remain independent of the CLI so a future UI
can configure them without constructing Docker arguments directly:

- **Project:** the canonical host path explicitly selected for a run by the
  user. A future UI may remember recent choices for convenience, but that is not
  an additional security authority or required allowlist.
- **Tool profile:** an allowlisted image/tool set such as `generic` or `go`.
- **Agent/session:** one execution identity with its own persistent or ephemeral
  state. Parallel agents must not share writable state unless explicitly joined.
- **Grant:** an explicit capability attached to a project or run, such as
  network access or one narrowly scoped credential. Absence means denied.

The host-side Go code is the policy enforcement boundary. CLI and future UI
inputs are requests to that policy layer, not raw Docker configuration.

## Assets and trust boundaries

Treat these independently when reviewing a change:

1. **Host filesystem.** Only the selected workspace, the auth source file, and
   Docker-managed state are intentionally mounted. The workspace is writable by
   design, so its contents can be destroyed or exfiltrated by the agent. An
   explicitly supplied workspace path is considered user approval for that run.
   The host launcher also owns a private SQLite database at
   `~/.local/state/pact/pact.db`. It is never mounted into the container and
   persists run metadata, selected Codex lifecycle events, and full transcript
   snapshots until the user removes it. Transcripts may contain prompts,
   reasoning, workspace paths and contents, commands and output, file diffs,
   and tool arguments and results.
2. **Credentials.** The host auth file is mounted read-only, but its contents
   are copied to the persistent state volume and are readable by the agent.
   Read-only mounting prevents modification, not disclosure or exfiltration.
3. **Network.** Docker's default network is currently enabled. Disabling Codex
   web search does not disable shell tools or arbitrary outbound traffic.
4. **Container privilege.** Setup runs as container root; the agent runs as the
   remapped unprivileged user. Codex is configured for `danger-full-access`
   because the container is its external sandbox. Docker's default capabilities,
   seccomp policy, writable root filesystem, and resource behavior currently
   apply.
5. **Persistent state.** A single named volume is shared by all invocations and
   workspaces. This violates the agent-isolation goal: one agent may observe
   another agent's state. Resume is not currently exposed by the launcher.
6. **Supply chain.** Go and Node archives are versioned and checksum-verified.
   The Ubuntu base tag and default `@openai/codex@latest` installation are not
   immutable. The host launcher uses the pinned `go-sqlite3` module and requires
   CGO and a C compiler when built. Package-manager and image build downloads
   remain trusted inputs.
7. **Host Docker invocation.** Values are passed as argv, not evaluated by a
   shell. Still validate variants, paths, mounts, and option placement because
   Docker itself interprets their syntax.
8. **Run metadata.** Immediately before `docker run`, the host records the
   canonical workspace path, model, reasoning effort, Dockerfile variant, and
   start time. Once Codex starts a thread, the host records its thread and
   session ids, app-server user agent, and backing state volume. While the turn
   runs, the host stores authoritative item and turn lifecycle events, raw token
   usage updates, warnings, errors, and model events; high-volume streaming
   deltas are not stored. After the turn ends, the host requests and stores the
   full `thread/read` result when the connection remains usable. When Docker
   exits, it records the terminal status, available exit code, and completion
   time. An unavailable exit code remains null. A host crash or forced
   termination may leave a row in the `running` state and only a partial event
   record. The database contents persist until the user removes them.
9. **Launcher.** The `pact` binary built from `cmd/pact` is the sole supported
   entrypoint. There is no parallel shell implementation. The host communicates
   with the unprivileged Codex app server over Docker's stdin and stdout; setup
   diagnostics are kept on stderr so they cannot corrupt the protocol stream.

## Target contract

- The caller explicitly selects every host path exposed to the container. Do not
  infer or automatically mount parent directories, sibling projects, or common
  credential/configuration locations.
- Canonicalize and validate the selected workspace: it must exist, be a
  directory, and have mount-safe syntax. Make the resolved path visible to the
  user before launch in any future UI. Reject unsupported tool profiles from an
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
  state unless the user explicitly joins them.
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
  so the CLI and a future UI use the same policy API and persisted configuration.
- Keep prompts and model arguments as discrete protocol fields or argv entries;
  do not reconstruct a shell command string.
- For every containment control, add a test that demonstrates both permitted
  behavior and a denied escape or misuse case where practical.
- In terms of actual coding style: minimize change sets such that humans can review commits
- Only add validation code where it is needed, where an input could be wrong
- Do not pollute the code with overflow/underflow checks unless this scenario could happen in day to day running
- Do not add premature complexity
- Do not pursue 100% test coverage. Add tests only when they provide stable,
  high-value confidence without creating disproportionate maintenance work.
- Do not add automated tests that execute Bash scripts. Keep shell changes small
  and verify them with syntax checks and focused manual smoke tests instead.

## Verification

The minimum local check is:

```text
go test ./...
go vet ./...
```
