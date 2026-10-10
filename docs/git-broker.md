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
OpenSSH executables, using fixed options and host-controlled configuration.
It never runs Git or opens a workspace repository. Git's standard
[`core.sshCommand` and `gpg.ssh.program`](https://git-scm.com/docs/git-config)
interfaces preserve ordinary CLI workflows, including worktrees and submodules
whose remotes are explicitly allowed.

Forwarding an SSH agent would also avoid copying key bytes, but would expose a
general SSH signing capability. The broker instead exposes only:

- The public key and current signing identity.
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
Pact always starts the broker, even if the config file or `git` section is absent,
`sshKey` is omitted, or the key file does not exist yet. An absent `git` section
uses the zero-value Git settings. Pact supplies any configured name/email and installs the Git
helpers at startup. Signing and authenticated SSH fail until usable credentials
are configured on the host; adding the key enables subsequent requests without
restarting Pact. To make an unsigned local commit before then, explicitly use
`git -c commit.gpgsign=false commit`.
Relative key and known-hosts paths resolve against the Pact config directory;
`~/` resolves against the host user's home.

`knownHosts` defaults to the host's `~/.ssh/known_hosts`. Host-key checking is
strict: add keys whose fingerprints you have verified before starting Pact.
Neither unknown keys nor changed keys are accepted automatically. SSH config,
agent identities, proxy commands, interactive authentication, and caller
environment variables are not used by the broker.

`allowedRemotes` is a deny-by-default list, read for each new SSH request. An empty
list permits signing but no authenticated transport. Each entry grants fetch and push
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
operations. Edit `git.allowedRemotes` in the host config to add or remove access
during a session. Every broker request reads and validates the full Git config,
including the identity, SSH key, known-hosts path, and allowed remotes. No reload
command or broker restart is needed.
Server permissions and host-key checks still apply to listed remotes.

Missing, unreadable, or invalid config rejects all new broker requests until it
is corrected; old settings are not retained. Removing the `git` section disables
signing and transport. Emptying `allowedRemotes` only denies authenticated transport.
An operation already in progress finishes with the settings it started with.

The container's Git name and email are initialized at startup when configured.
After adding or changing the host identity, the agent should run
`pact-git-sign --refresh`, then retry its normal Git command. This fetches the
current name, email, and public key, and updates the local Git identity and
verification files. It is safe to repeat: unchanged files are left alone and
existing signer entries are not duplicated. No shell or Pact restart is needed.
The broker rejects signing with the old identity immediately.
Key changes apply immediately to signing and SSH authentication. The signing helper
refreshes identity and public verification material before signing or verifying; startup only
configures Git and does not contact the broker.
It fetches the current public key and identity together through `signing-identity`,
so the allowed-signers file uses the host's current email, not the startup email.
Git reads the identity from `~/.config/pact/git-identity.config` through a
command-scope include, which overrides repository-local identity settings.
The explicit refresh is needed before constructing a commit when the identity
was initially missing or changed: Git constructs the payload before invoking
the signing helper. Refresh never rewrites an already constructed commit.

Refreshes serialize updates and atomically replace each changed file.
Previously trusted email/key pairs are retained in
`~/.config/pact/git-allowed-signers`, so
historical signatures still verify after rotation. Rotation does not revoke that
historical trust: remove obsolete entries manually if necessary. Emails containing
SSH principal pattern syntax or whitespace cannot be cached as literal signers.
If refresh fails, signing fails; verification warns and uses cached public keys.
Verification itself stays local. Only MCP registration changes require a restart.

All helper HTTP requests share a 30-second response-header timeout and a
30-minute overall timeout, allowing long-running Git transfers.

The config and its directory, key files, known-hosts files, and host executables
must resolve outside the mounted workspace. Pact checks host executables at
startup and checks config and credential paths, including symlinks, on each
request. Keep all copies and hard links of credentials outside the workspace too.
Do not mount your home directory as the workspace.

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
revocation. The allowed-remotes command is checked against a real broker, including
live updates, empty lists and connection failures. The Git workflow also checks
that host config edits grant and revoke transport access, rotate keys and
known-hosts paths, and change the signing identity without a broker restart.
Invalid config and workspace symlinks must fail without starting a host process.
Tests do not require a privileged SSH daemon.

On a host where an unprivileged OpenSSH daemon can run,
`PACT_TEST_SSHD=1 ./shell go test ./internal/gitbroker -run TestGitWorkflow -count=1`
runs the same workflow over real SSH and verifies rejection of an unknown host
key. The temporary daemon listens only on loopback and uses generated test keys.
