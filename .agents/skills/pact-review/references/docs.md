# Documentation review patterns

## PACT-DOCS-001: Keep the README useful to operators

README changes should explain the contract between the operator and Pact.
Put internal architecture and implementation details in the established `docs/`
documents. Identify what task or decision a paragraph helps the operator perform;
do not add internal details merely because a feature changed.

Sources: [internal image details do not belong in README](https://forge.alexghr.me/ag/pact/pulls/28#issuecomment-168),
[omit obvious help/version explanation](https://forge.alexghr.me/ag/pact/pulls/107#issuecomment-926).

Commands, setup, limitations affecting use, and supported capabilities can
belong in the README. Internal terminology alone does not make an explanation
irrelevant. Review code changes for user-visible contract changes that need
an update under this same rule.

Pattern from PR 107: the added paragraph started with "Run `pact version` to
see the launcher version" and was rejected as unnecessary help/version guidance.
Compare its reader action with configuration instructions for allowed Git
remotes, which tell the operator how to enable a capability. Identify the action
and its necessity in the changed paragraph; mentioning a CLI command alone does
not justify placement. Put an explanation of build-version construction in an
existing internal document when that explanation is needed.

## PACT-DOCS-002: Make non-obvious protocol data understandable

When changing a protocol or payload whose shape is hard to infer, include a
small concrete data example near its explanation. Check the example against the
implementation at the reviewed revision and explain the ambiguity it resolves.

Source: [example of expected Git object data](https://forge.alexghr.me/ag/pact/pulls/3#issuecomment-14).

An example is useful when it clarifies the contract; do not demand one for every
type, helper, or obvious argument. This rule does not authorize rewriting
unrelated comments or documentation.

The accepted example is beside `validObject` in `internal/gitbroker/broker.go`:
concrete commit/tag headers show which committer or tagger the broker checks.
For a changed payload, compare its producer/consumer with that explanatory role.
Name the field, framing, or ordering ambiguity that a proposed example resolves.
An obvious single-value argument does not need the same treatment.
