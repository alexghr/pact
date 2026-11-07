---
name: pact-review
description: Supply Pact-specific review patterns and examples learned from ag/pact PRs. These patterns do not govern other repositories running inside Pact.
---

# Pact review patterns

These patterns apply to `ag/pact` at `forge.alexghr.me`, the codebase whose Go module
is `github.com/alexghr/pact`. Resolve repository identity through the forge and
Git metadata; the checkout's directory name is insufficient. Do not apply it
to another repository just because Pact runs the session.

Use `review-pr` for the PR workflow when available. This skill supplies patterns
and references to the coordinator and concern reviewers; it does not launch a
second coordinator. It can also supply policy for a local branch review.
Read the `AGENTS.md` at the selected policy revision for current conventions.

## Scope and routing

Use these learned patterns to focus investigations and recognize recurring
problems. They are not an exhaustive checklist or a limit on findings. Reviewers
can raise other concerns relevant to the PR using their own judgment and current
evidence. The operator decides which suggestions to implement or ignore.

Read relevant PR discussion for context. A previous rejection or deferral is
not a standing prohibition on raising a concern. Link that discussion and
explain the concern's relevance to the current change.

| Concern | Capability | Pattern reference |
| --- | --- | --- |
| Ownership, APIs, control flow, approval contract | `review-design` | [Design](references/design.md) |
| Test behavior and use of real components | `review-tests` | [Tests](references/tests.md) |
| Documentation audience and protocol examples | `review-docs` | [Documentation](references/docs.md) |

The coordinator's behavioral correctness pass remains part of the review even
without a matching Pact pattern. Use relevant patterns to guide investigations;
group unaffected patterns by concern with a brief reason. Code-only changes
can affect documentation. For small changes the coordinator can perform these
investigations itself; otherwise delegate by concern, not by rule. Give workers
the pinned references and relevant context explicitly.

Run checks through `./shell p-ci --report FILE`, with `PACT_CI_BASE` set to the
recorded diff base. Keep check results separate from semantic findings. Optional
real SSH, Docker, and Codex integrations have their documented prerequisites;
report what was actually run. Use real components for any additional
reproduction, following the testing guidance in `AGENTS.md`.

`p-ci` covers dependencies, package layout, new cmd test files, config JSON
naming, direct Docker commands, formatting, build, and tests. Keep those results
in the check report; explain additional semantic concerns rather than repeating
check diagnostics as inline findings.

For each finding, show the relevant source, evidence, impact, and suggested
correction. Cite a pattern ID and its history when one informed the finding;
other findings need no pre-existing pattern. Explain assumptions or uncertainty.

Maintain reusable patterns and examples through ordinary repository changes,
using comments, replies, and resulting code as source context. Keep PR-specific
acceptance, rejection, and deferral in the PR discussion.
