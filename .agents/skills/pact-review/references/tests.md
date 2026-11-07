# Test review patterns

## PACT-TEST-001: Protect observable application behavior

For added or changed tests, identify the concrete Pact bug they would catch.
Avoid tests that just repeat constants, flags, command construction, or upstream
library behavior. A useful test should continue passing when implementation
changes preserve the behavior it protects. Consider existing coverage before
adding another test of the same failure mode.

Sources: [custom logic versus Docker arguments](https://forge.alexghr.me/ag/pact/pulls/2#issuecomment-6),
[remove a trivial command test](https://forge.alexghr.me/ag/pact/pulls/28#issuecomment-172).

Executable-boundary tests can be appropriate for exit status, standard streams,
and signals. A config test can protect meaningful precedence or validation
logic. Explain the specific failure rather than classifying every command,
config, or parsing test as unnecessary. The cmd check handles new test-file
placement; this rule assesses the behavior tested.

## PACT-TEST-002: Exercise real components

Use actual files, repositories, sockets, subprocesses, and services. Minimal
protocol endpoints are permitted by `AGENTS.md`: a small HTTP handler can return
controlled replies while exercising Pact's real client or proxy. Do not flag
such an endpoint merely because the test controls its responses. Check that it
stays focused and introduces no dependencies solely for tests; adding or building
a full OAuth2 server to test an MCP integration is excessive.

A fake Docker executable on PATH remains a forbidden double. For other cases
where real components are too heavy or unavailable, follow `AGENTS.md` and
discuss the test instead of substituting a mock. Removing an unnecessary test
can be the right outcome; do not demand an integration test without a concrete
failure to protect.

Sources: [fake Docker test rejected](https://forge.alexghr.me/ag/pact/pulls/28#issuecomment-172),
[operator review on mocks/fakes](https://forge.alexghr.me/ag/pact/pulls/2#issuecomment-9).

Temporary real resources and isolated test data are appropriate. Look at what
the test replaces, not whether it calls something a fixture or uses a process.
If both test rules describe one unnecessary fake-based test, return one finding
with the primary rule and mention the related concern in its evidence.
