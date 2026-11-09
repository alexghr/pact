# MCP approval protocol

Pact sends approval-required MCP calls to an external service and waits for a
per-call decision before using host credentials to forward the original request.
The service owns its UI and rules. Pact includes terminal and macOS dialog
services; other apps or daemons can use the same protocol.

## macOS dialogs

Build with `./shell p-build`, then run the companion on your Mac:

```sh
mkdir -p -m 700 ~/.config/pact
./pact-approvals --socket ~/.config/pact/approval.sock
```

Omit `--socket` to use `approvals.socket` from the default Pact config, with
the same path resolution as `pact approvals` below. Run either service on a
given socket. For Pact running on another machine, use the SSH forwarding
example under [Transport](#transport).

Each native dialog shows the host, session, tool, expiry, and complete request
in a scrollable text view. Its four actions are:

- **Approve:** allow this call once.
- **Approve server/tool for session N:** allow that exact tool, with any
  arguments, for the session.
- **Approve server/* for session N:** allow every tool under that MCP server
  for the session.
- **Deny:** reject this call. Return and Escape also deny.

Grants are held only in memory. They use the hostname and stable session key,
so they survive session resumes while the service runs. Restart the service
to clear all grants. There is no session-end notification in the protocol;
grants remain until the service exits.

Requests are handled one at a time, checking remembered grants before showing
a dialog. Queued calls keep their original expiry. Disconnecting, expiry, or
Ctrl+C dismisses the active dialog. The default approval timeout is 60 seconds;
set `approvals.timeoutSeconds` on the Pact host if you want more time.

The companion runs as a regular host process in your logged-in macOS desktop.
It uses the built-in `osascript` and AppKit; it needs no application bundle,
extra dependencies, or persistent rule storage.

## Terminal proof of concept

After setting `approvals.socket` in the default Pact config below, run this on
the host in a separate terminal before starting a Pact session:

```sh
mkdir -p -m 700 ~/.config/pact
pact approvals
```

The command uses `approvals.socket` from `$XDG_CONFIG_HOME/pact/config.json`,
defaulting to `~/.config/pact/config.json`. Relative socket paths resolve against
the config directory; `~/` uses your home directory. Use `--socket PATH` to
override the socket or run without a config file.

The command prints each complete request, including session context and tool
arguments. Enter `y` to approve or `n` to reject, followed by Enter. Other input
repeats the prompt. Each answer applies to one call; decisions are not saved.

Prompts run one at a time. If the caller disconnects or the request expires
while a prompt is waiting, the command cancels the input read and dismisses
the prompt automatically. No Enter or answer is needed before the next prompt.
Queued requests retain their original expiry.
Ctrl+C stops the listener; EOF at a prompt also stops it. Pending calls fail
without approval. The command creates a mode-0600 socket, removes it on exit,
and refuses to replace an existing path. Keep its parent directory private.

## Configuration

```json
{
  "approvals": {
    "socket": "~/.config/pact/approval.sock",
    "timeoutSeconds": 60
  },
  "mcp": {
    "forgejo": {
      "url": "https://forge.example.com/mcp",
      "tokenFile": "forgejo-token",
      "allowedTools": ["^(get|list)_"],
      "approvalTools": ["^(create|update|delete)_"]
    }
  }
}
```

`approvalTools` uses the same Go regex syntax and tool names as `allowedTools`.
Approval matches take precedence over automatic allowance. The first matching
approval regex becomes `call.rule`; a tool needs only an approval match to be
eligible. Calls matching neither list are rejected without contacting the service.
Invalid regexes or `approvalTools` without an approval socket prevent startup.
Other MCP methods retain their existing behavior.

`socket` names the service's Unix socket on the Pact host. Relative paths resolve
against the Pact config directory; `~/` uses the host user's home directory.
Pact does not create or mount this socket. It rejects paths within the workspace,
including symlinks into it when connecting. Keep the socket and its parent
directory private to the operator and outside all agent mounts. The service may
be absent at startup; an approval-required call then fails without executing.

`timeoutSeconds` defaults to 60 when omitted or zero and accepts 1 through 3600.
Config changes take effect on start or resume. With an approval service configured,
Pact gives Pi the approval timeout plus 60 seconds for MCP execution.

Pact's HTTP path allows this wait: the host bridge has a five-second request-header
timeout but no request-body or response-write timeout. The approval transport has
no separate response-header timeout; the request context enforces the approval
deadline. The reverse proxy and container relay impose no response wait limit.
The external approval service must also allow a response to remain pending for
the configured interval.

## Transport

Pact is an HTTP/1.1 client over the Unix socket. For each call it opens a separate
connection and sends `POST /v1/approvals` with `Host: pact`,
`Content-Type: application/json`, and `Accept: application/json`. The hostname is
synthetic; there is no DNS lookup or TCP fallback. `/v1/` versions the contract.
There is one operation, so HTTP supplies request/response correlation, status,
and cancellation without a JSON-RPC method envelope. The opaque approval ID
additionally binds the decision to the pending invocation.
Receivers should accept request bodies up to 2 MiB, including the MCP arguments
and session metadata; Pact's incoming MCP messages remain limited to 1 MiB.

The service holds the response until it has a decision. Concurrent calls use
independent connections and IDs, with no ordering assumption. Authentication is
provided by Unix socket access and, for remote approval UIs, SSH. The socket is
separate from the container-facing host bridge, including when using host networking.
Application-level authentication is tracked in [#106](https://forge.alexghr.me/ag/pact/issues/106).

An eventual laptop service can listen locally and use OpenSSH's
[`-R remote_socket:local_socket`](https://man.openbsd.org/ssh.1#R) forwarding.
For example, from the laptop:

```sh
ssh -N -o ExitOnForwardFailure=yes \
  -R /home/alex/.config/pact/approval.sock:/Users/alex/.config/pact/approval.sock \
  work-server
```

The remote path goes in Pact's config; the local path belongs to the approval
service. Create private parent directories first. The SSH server must permit
Unix socket forwarding; manage any stale forwarding socket before reconnecting.
Pact never needs access to the laptop's desktop or an agent harness approval API.

## Request

```json
{
  "id": "Y7E3QKB2ZQZ6CJSRMKTDG4L2UA",
  "hostname": "work-server",
  "session": {
    "id": 42,
    "key": "5V2XQF7M4SKJ3ZQCDLNVW2EBMA",
    "executionId": 17,
    "title": "Fix deployment",
    "workspace": "/home/alex/work/project",
    "backend": "pi"
  },
  "call": {
    "server": "forgejo",
    "tool": "create_issue",
    "arguments": {
      "owner": "team",
      "repo": "project",
      "title": "Investigate deployment"
    },
    "rule": "^(create|update|delete)_"
  },
  "createdAt": "2026-11-01T16:00:00Z",
  "expiresAt": "2026-11-01T16:01:00Z"
}
```

- `id` is opaque and unique to this approval attempt. It is independent of the
  agent's JSON-RPC ID. Repeating an identical call creates another approval.
- `hostname` is read from the Pact host automatically. It helps identify the
  environment sending the request and is not an authenticated peer identity.
- `session.id` is the numeric Pact ID shown to the operator. `session.key` is
  the stable, opaque session identity, retained across resumes; use it for
  session-scoped client rules. `executionId` identifies the current run.
- `title`, `workspace`, and `backend` provide display context, not authorization.
- `call.server` is the host-configured MCP name on the sending host.
  `tool` is the upstream name without a client prefix. `arguments` contains
  the complete JSON arguments, using `{}` if the agent omitted them.
- `rule` identifies the approval regex that matched. Timestamps use UTC RFC 3339;
  `expiresAt` is the earlier of the configured timeout and the caller's deadline.

Pact does not send upstream tokens, token file paths, HTTP headers, or endpoint
URLs. Arguments can themselves contain sensitive data needed for the decision.
Opaque bearer credentials cannot establish the upstream account's identity:
the operator must review stored client rules when changing a server's target or
account. Hostnames alone do not establish account identity.

## Response and lifecycle

Return HTTP 200 with `Content-Type: application/json` and one of:

```json
{"id":"Y7E3QKB2ZQZ6CJSRMKTDG4L2UA","decision":"approve"}
```

```json
{"id":"Y7E3QKB2ZQZ6CJSRMKTDG4L2UA","decision":"reject"}
```

The ID must exactly match the request and the decision must be `approve` or
`reject`. Responses are limited to 4 KiB. Unknown response fields are ignored
for additive extensions; unsupported decision values never authorize a call.

Approval releases only the original buffered MCP request, with its original
arguments, once. A rejection returns HTTP 403 and a JSON-RPC error to the agent.
An unreachable service, disconnected connection, malformed or mismatched reply,
non-200 status (including redirects), or timeout returns HTTP 503 and never
forwards the call. Service response bodies and transport error details are not
reflected to the agent. The host enforces its own deadline independently of the
service's clock.

Pact cancels the approval HTTP request when the MCP caller disconnects or its
bridge shuts down. The service should dismiss the pending dialog on disconnect
or expiry and must not treat a late decision as an authorization for another call.
A successful approval response is not an acknowledgement of upstream execution
or success. There are no automatic retries or guarantees across agent retries:
another MCP call gets a new approval ID and may repeat an earlier side effect.

Pact stores neither decisions nor grants. To implement "always approve this
action in this session", the service stores its own rule, evaluates subsequent
requests against it, and returns an ordinary per-call `approve`. Durable client
rules and rule UI are separate from the terminal proof of concept.
