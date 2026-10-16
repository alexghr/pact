# Project guidance

Prefer small, concise code that a human can read and understand.
Keep changes focused. Every abstraction, dependency, and test should
justify the complexity it adds.

## Documentation

Keep the README user-facing. Only modify if the contract between the
operator and Pact changes. Do not put internal docs in the README.

For major features write documentation for humans and agents in `./docs/`.

## Development

Use the project's devenv environment:

- Run checks with `./shell p-ci`.
- Build the launcher with `./shell p-build`.

Relevant integration checks are opt-in and are not enabled by `p-ci`:

- Real SSH, when an unprivileged SSH daemon can run:
  `PACT_TEST_SSHD=1 ./shell go test ./internal/gitbroker -run TestGitWorkflow -count=1`
- Docker Desktop, when its daemon and the local `pact-codex` image are available:
  `PACT_TEST_DOCKER=1 ./shell go test ./internal/hostbridge -run TestTCPAuthenticationAndLifetime -count=1`

Use Conventional Commits. Do not use scopes. Commit body should be empty.
Commit titles should be concise.

Allowed commit types: "feat", "fix" and "chore".

Keep commits atomic. Aim for at most 1,000 changed lines (additions + deletions)
per commit.

Pull requests should contain a single reviewable commit. If you are implementing
code review findings, amend the existing commit.

If a feature requires multiple commits, use stacked branches and pull requests. There may
be helper skills loaded in your environment to work with stacked branches. Use them.

## Implementation and Go style

- Prefer straightforward control flow: ordinary loops, early returns,
  and shallow nesting.
- Use the standard library before adding dependencies or custom machinery.
- Use modern Go idioms and APIs supported by `go.mod` when they simplify
  the code, such as `slices`, `strings.Cut`, and `errors.AsType`.
  Limit modernization to new code and code you edit; keep unrelated
  refactoring separate.
- Prefer concrete types. Introduce interfaces for actual consumers or
  multiple implementations, never solely to enable mocking.
- Extract helpers when they clarify a distinct operation or remove
  meaningful repetition. Avoid layers of tiny forwarding functions,
  generic frameworks, and speculative extension points.
- Use concise, meaningful names and explicit error handling. Add error
  context where it helps diagnose a failure; preserve error identity.
- Make ownership and cleanup clear for goroutines, listeners, processes,
  and temporary resources. Propagate cancellation and bound cleanup.
- Comments should explain intent, constraints, and non-obvious wiring.
  Include a small data example when a protocol or payload is hard to infer.

## Testing

Tests should test logic and observable behavior, not declarations.
They should survive implementation changes that preserve behavior and usually
change less often than the code they test. If test code churn approaches
implementation churn in an area, reconsider whether the tests mirror the
implementation.

Before adding a test:

1. Identify the concrete bug or failure mode it would catch.
2. Read existing tests and decide whether extending one is enough.
3. Choose the smallest test that exercises the real behavior.

- Do not add tests solely for coverage or one test file per module.
- Do not add tests that merely restate constants, YAML/JSON declarations,
  straightforward file reads or parsing, argument forwarding, command
  construction, or expected flag lists. Test config handling when it contains
  meaningful logic prone to breakage.
- Test the resulting behavior instead, e.g. a signed commit verifies,
  credentials cannot be overridden or exposed, a stream is cancelled,
  or a forbidden request cannot start a host process.
- Do not use mocks, fakes, stubs, or other test doubles. This includes
  scripts or binaries that impersonate external tools, such as a fake
  `docker` placed on `PATH`.
- Use real components, temporary files and repositories, sockets, and
  subprocesses. If the actual components are too complicated, heavy, or
  unavailable, discuss the test with the user before writing it. Do not
  substitute a test double without explicit agreement.
- Do not duplicate a scenario across test layers unless each test catches
  a distinct failure. Keep fixtures, helpers, and case tables small.
- Use `t.TempDir`, `t.Cleanup`, and `t.Context` where appropriate.
  Synchronize on events or readiness; avoid fixed sleeps.
- Report validation actually performed, including skipped integration
  checks and their limitations.

## Project conventions

- Keep all Go implementation packages under `internal/`. Do not add public
  packages unless the user explicitly requests a public Go API.
- Keep executable entrypoints under `cmd/` and container build inputs
  under `internal/nix/context/`. Use the existing build scripts.
- Reuse upstream Nix packaging instead of maintaining duplicate recipes.
- Use camelCase JSON fields consistently. Keep configuration, errors,
  documentation, and examples aligned.
- Preserve Pact's host/container boundary: Git keys and upstream MCP
  credentials stay on the host. Host broker code must not execute Git
  against the agent-writable workspace.
