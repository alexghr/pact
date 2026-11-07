# Design review patterns

These Pact-specific patterns guide investigations alongside current `AGENTS.md`.
Reviewers can also raise other relevant concerns supported by current evidence.
The locations below are starting points in current Pact; find the equivalent
operation at the reviewed revision when replaying older PRs.

## PACT-DESIGN-001: Own operations behind concrete package APIs

When moving or extending application logic, keep the operation in its owning
internal package. Expose concrete operations so callers need not reconstruct
commands or manipulate the package's implementation details. Inspect actual
callers: moving a generic command runner into a package can leave the same
responsibility scattered across its consumers.

Sources: [move image logic out of cmd](https://forge.alexghr.me/ag/pact/pulls/28#issuecomment-169),
[concrete Docker API](https://forge.alexghr.me/ag/pact/pulls/88#issuecomment-765),
[keep bind-mount quotation internal](https://forge.alexghr.me/ag/pact/pulls/88#issuecomment-764).

The layout check detects misplaced packages and the source check detects direct
Docker commands. This rule addresses semantic ownership beyond those checks.
It does not require a public wrapper for every low-level operation or forbid
useful private helpers.

Pattern from PR 88: a caller assembling arguments for `docker.Output` still owns
Docker CLI details despite using the docker package. Compare that shape with
`docker.CreateVolume` in `internal/docker/volumes.go` or `ContainerConfig.Mounts`
in `internal/docker/containers.go`: callers supply intent, and the package builds
arguments and quotes mounts. Trace a changed caller through both sides before
concluding ownership moved. A private command builder inside the owner is fine.

## PACT-DESIGN-002: Keep case handling straightforward

For changed selection or matching logic, prefer a direct loop or explicit named
cases over extra intermediate collections and indirection. Stop when the needed
match is established. A finding must show the redundant work or control flow
in this change and describe a simpler equivalent operation.

Sources: [regular matching loop](https://forge.alexghr.me/ag/pact/pulls/26#issuecomment-154),
[return on the first match](https://forge.alexghr.me/ag/pact/pulls/26#issuecomment-157).

Do not turn these examples into a ban on collection helpers or unrelated
modernization. Some operations intentionally collect all matches.

## PACT-DESIGN-003: Validate the contract Pact actually owns

Treat native thread references as opaque strings rather than imposing UUID
syntax. Avoid broad path heuristics that reject otherwise valid operator-selected
workspaces. For other changed validation, establish the owned invariant and
upstream behavior before claiming that a guard is redundant.

Sources: [opaque thread IDs](https://forge.alexghr.me/ag/pact/pulls/44#issuecomment-389),
[overbroad workspaces-name rejection](https://forge.alexghr.me/ag/pact/pulls/76#issuecomment-678).

This is not permission to remove checks protecting a real host/container boundary
or to accept ambiguous approval requests. A bad historical heuristic does not
make every path check unnecessary.

## PACT-DESIGN-004: Reuse a shared operation when repetition is real

When adding another instance of existing transport setup or lifecycle logic,
inspect whether the existing owner can expose the shared operation. Explain
the repeated responsibility and current consumers rather than proposing an
interface solely for testability or future possibilities.

Sources: [share HTTP-over-Unix dialing](https://forge.alexghr.me/ag/pact/pulls/104#issuecomment-902),
[duplicated binding publication](https://forge.alexghr.me/ag/pact/pulls/93#issuecomment-823).

Keep distinct lifetimes and error behavior visible. Similar syntax alone is not
enough to justify an abstraction.

Pattern from PR 104: compare Unix HTTP setup in `internal/approval/client.go`
with `internal/codex/appserver.go`. Current code shares `internal/unixhttp.Dialer`;
the approval caller supplies its additional path check. A proposed shared helper
must preserve that difference. Return the two call sites and the repeated or
distinct operation, rather than just calling the code "duplicated."

## PACT-DESIGN-005: Approve the same MCP action that is forwarded

For changes to MCP approval parsing and forwarding, compare the tool and
arguments shown to the approver with those interpreted by upstream. Exact JSON
member names and ambiguity matter when the proxy forwards the original bytes.

Sources: [case-insensitive parsing bypass](https://forge.alexghr.me/ag/pact/pulls/104#issuecomment-894),
[operator-confirmed resolution using json/v2](https://forge.alexghr.me/ag/pact/pulls/104#issuecomment-907).

The contract is the pattern; use of a particular JSON package is not itself
proof of safety or a violation. Other concerns in the approval lifecycle can
also be raised on their own evidence, with relevant prior discussion linked.

Pattern from PR 104: `{"arguments":{"repo":"production"},"Arguments":{"repo":"sandbox"}}`
exposed different member selection by the old parser and the upstream. Trace
the message in `internal/mcpproxy/proxy.go` through `Policy.Approve` and the
forwarded body. Determine which value each side uses at this revision; do not
assume the historical bypass still exists.
