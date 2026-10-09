# Git credential isolation

Pact keeps the configured private SSH key on the host. Normal Git commands run
inside the container. A small SSH transport helper streams Git's wire protocol
through a host broker, and a signing helper sends Git object bytes to the host.
Only the public signing key, allowed remote URLs, and a session-scoped broker
endpoint enter the container. No private-key mount, copied key, credential
helper, or SSH agent socket is provided.

## Why a transport and signing broker

A host Git MCP could provide clone, commit, fetch, and push, but running Git in
an agent-writable repository on the host would also trust its hooks, filters,
configuration includes, external commands, and filesystem paths. Safely supporting
the whole Git command surface would require a separate repository isolation layer.

This broker keeps that work in the existing container. The host runs only its own
OpenSSH executables, using fixed options and configuration chosen before startup.
It never runs Git or opens a workspace repository. Git's standard
[`core.sshCommand` and `gpg.ssh.program`](https://git-scm.com/docs/git-config)
interfaces preserve ordinary CLI workflows, including worktrees and submodules
whose remotes are explicitly allowed.

Forwarding an SSH agent would also avoid copying key bytes, but would expose a
general SSH signing capability. The broker instead exposes only:

- The public key.
- The allowed remote URLs.
- Signing of commit/tag payloads up to 1 MiB, with the configured committer/tagger.
  The author may differ for cherry-picks. OpenSSH's fixed
  [`git` signature namespace](https://man.openbsd.org/ssh-keygen#Y~4) separates
  these signatures from SSH authentication and other signing uses.
- `git-upload-pack` and `git-receive-pack` for exact configured SSH destinations
  and repository paths. Requests cannot select arbitrary SSH options or commands.

Transport replies carry stdout, stderr, and an explicit exit status. An interrupted
stream fails, rather than appearing to be a successful push. Host Git processes
are never involved in staging, hooks, filters, merges, or checking out files.

## Configuration

```json
{
  "git": {
    "name": "Pact Agent",
    "email": "agent@example.com",
    "sshKey": "~/.ssh/pact_ed25519",
    "knownHosts": "~/.ssh/known_hosts",
    "allowedRemotes": [
      "ssh://forgejo@forge.example.com/team/project",
      "git@github.com:team/library.git",
      "ssh://git@forge.example.com:2222/team/another-project"
    ]
  }
}
```

`sshKey` must be an unencrypted OpenSSH private key. The host needs `ssh` and
`ssh-keygen` on PATH. Name and email are required when a key is configured.
Without `sshKey`, Pact still supplies the configured name/email for local Git.
Relative key and known-hosts paths resolve against the Pact config directory;
`~/` resolves against the host user's home.

`knownHosts` defaults to the host's `~/.ssh/known_hosts`. Host-key checking is
strict: add keys whose fingerprints you have verified before starting Pact.
Neither unknown keys nor changed keys are accepted automatically. SSH config,
agent identities, proxy commands, interactive authentication, and caller
environment variables are not used by the broker.

`allowedRemotes` is a deny-by-default list, read once at startup. An empty list
permits signing but no authenticated transport. Each entry grants fetch and push
to that repository, subject to the Git server's permissions. Use the exact URL
spelling used by Git, including the user, optional port, and
leading slash: `user@host:team/repo` and `ssh://user@host/team/repo` have different
SSH paths. A single trailing `.git` is optional for matching; both spellings
connect to the configured path, so aliases cannot grant access to a second
repository on hosts that distinguish the two. The first matching entry is used if
both spellings are configured. Rejections distinguish unsupported commands from
remote mismatches and identify the requested destination.
No host aliases, wildcard repositories, IPv6 literals, URL passwords,
HTTPS credential forwarding, `.`/`..` components, or paths with shell
metacharacters/spaces are supported. Public HTTPS Git operations still work
directly from the container. SSH transport uses Git protocol v0.

When Git brokering is enabled (`PACT_GIT_URL` is set), the agent can inspect the
session's configured remote URLs, one per line:

```sh
pact-git-ssh --allowed-remotes
```

The command queries the host broker on each invocation, preserving each
configured URL's spelling and order. Successful empty output means no
authenticated SSH remotes are allowed; an unavailable broker returns an error.
The list comes from the same configuration the broker uses to authorize SSH
operations. Config is currently read at startup: update `git.allowedRemotes` on
the host and start a new Pact session to change access. Future config reloads can
update the broker's list without changing the agent's environment or this
command. Server permissions and host-key checks still apply to listed remotes.

Key files, known-hosts files, and host executables must resolve outside the
mounted workspace. Pact checks this at startup, including symlinks. Keep all
copies and hard links of credentials outside the workspace too. Do not mount
your home directory as the workspace.

## Boundary and lifecycle

The broker is registered on the existing private host bridge as `/mcp/pact_git`,
but speaks a small internal HTTP protocol, not MCP. `pact_git` is reserved when
Git brokering is enabled. It is not advertised as an agent MCP server. Configured
MCP servers keep their current registrations, tools, and forwarding behavior.
This does not restrict what an independently configured MCP server can do.

Linux uses the existing private Unix socket and container loopback relay. macOS
Docker Desktop uses the bridge's random session URL. Closing the bridge revokes
access and cancels subprocesses. Host subprocesses run outside the workspace with
a minimal environment. Signing and transport have time and concurrency limits;
Git pack data is streamed, not buffered in host memory.

An agent can sign arbitrary changes under the configured identity and push to
allowed repositories while the session is active. This is credential isolation,
not per-change approval or branch protection. Apply repository permissions and
branch protections on the Git server if needed. Workspace files remain
agent-writable, including `.git`; this mechanism does not make it safe to run
arbitrary repository commands on the host afterward.

## Upgrading

Config fields now consistently use camelCase, matching MCP's `tokenFile`.
Rename `git.ssh_key` to `git.sshKey`, `git.known_hosts` to `git.knownHosts`, and
`git.allowed_remotes` to `git.allowedRemotes` if present; the old spellings are
no longer read. Name and email settings are unchanged. Add
`allowedRemotes` for repositories that should support authenticated operations,
and populate the host's known-hosts file before starting the updated launcher.
The image is rebuilt by Pact as usual. Existing sessions still have the old key
mount and must be stopped. Previous versions exposed the full private key; this
change cannot revoke copies already made. Rotate that key if it may have been
retained or disclosed by an earlier session.

## Building the helpers

`cmd/git-client/main.go` builds one credential-free executable. The image installs
it as `/usr/local/bin/pact-git-sign` and creates a `pact-git-ssh` symlink beside it.
The executable name selects signing or SSH transport, respectively. Both commands
are on the agent's PATH and are configured by `git-identity.sh`.

`./shell p-build` first runs `p-git-client`, which cross-compiles static Linux
helpers for amd64 and arm64 into `internal/nix/context/bin/`. The launcher embeds
the whole `internal/nix/context/` directory. Docker selects the prebuilt helper
using `TARGETARCH`, so image builds do not need Go or the helper's source code.
This also works when the launcher is built on macOS. Generated helpers are ignored
by Git; use `p-build` to regenerate them whenever building the launcher.

Run `./shell p-ci` and `./shell p-build`. Tests verify signed commits/tags, real
Git clone/fetch/push through the bridge and relay (with a local Git service fixture
in place of the SSH network hop), rejected signing/transport requests, and bridge
revocation. The allowed-remotes command is checked against a real broker,
including empty lists and connection failures. Tests do not require a privileged
SSH daemon.

On a host where an unprivileged OpenSSH daemon can run,
`PACT_TEST_SSHD=1 ./shell go test ./internal/gitbroker -run TestGitWorkflow -count=1`
runs the same workflow over real SSH and verifies rejection of an unknown host
key. The temporary daemon listens only on loopback and uses generated test keys.
