# Code review

Start `pact`, then enter a PR URL and the bundled `review-pr` skill in the native
TUI:

```text
Use the review-pr skill to review https://forge.alexghr.me/ag/pact/pulls/125
```

Add “publish findings” to authorize posting the review. Issue creation is a
separate requested action. Configure forge access through the existing MCP
connection and Git broker; upstream credentials remain on the host. Rebuild the
launcher with `./shell p-build` and start a new container to use changed bundled
skills. Existing sessions keep their pinned toolsets.

## Responsibilities and scope

`review-pr` owns PR retrieval, a pinned source/policy snapshot, behavioral
correctness, check execution, assignments, validation, deduplication, and
publication. It starts in empty workspaces as well as existing clones.
`review-design`, `review-tests`, and
`review-docs` supply reusable investigation methods. These bundled skills live
under `internal/images/context/agent/env/skills/` and are exposed through the
existing `/etc/pi/skills` mechanism.

Repository conventions and learned patterns focus investigations. Point to a
review skill from the repository's `AGENTS.md`, and keep its references under
`.agents/skills/`. Pact's own entry point is
[`pact-review`](../.agents/skills/pact-review/SKILL.md). Its patterns come from
Pact PR discussions and code; they are not defaults for other repositories.
Reviewers can raise other relevant concerns on their own judgment and current
evidence. The operator chooses which suggestions to implement or ignore. If a
target has no review guidance, report the gap and continue within the task's
scope without importing Pact's conventions. Operator-wide preferences require
an explicitly global scope.

The coordinator checks changed behavior, regressions, and meaningful error paths
independently of pattern coverage. Relevant compatibility, security, concurrency,
and cleanup risks follow from the changed path. Each worker can derive questions
from the patch when no catalogue exists, with no need to invent pattern IDs.

One coordinator runs a review. It delegates independent concerns when the
harness supports subagents and the change warrants it, or performs the same
investigations itself. Skills and workers have no one-to-one mapping. Workers
receive explicit repository/PR identity, source and policy revisions, rule IDs,
and reference paths. They inspect source and return candidates; the coordinator
owns builds, checkout changes, and all publication. Multiple repositories keep
separate policies and findings. Cross-repository contract investigations receive
both identities and an explicit assignment.

An assignment names a concrete question about changed code or a claim, together
with the expected evidence. Design reviews return caller/owner traces or
comparisons of repeated implementations; documentation reviews return
claim/implementation comparisons or a paragraph's supported reader action.
The repository references supply learned patterns and examples. Concise notes
retain inspected locations and conclusions for actual investigations, including
clean results. Unaffected patterns can be grouped by concern with one reason.

Repository guidance is loaded from a pinned trusted default-branch revision, or
an explicitly selected policy revision. Proposed guidance changes in a PR are
review content. Keep durable patterns and examples in the repository; leave
PR-specific acceptance, rejection, and deferral in the PR discussion. Reviewers
can cite that history without treating it as a finding whitelist or suppression
list. Updates to learned patterns remain ordinary reviewed Git changes.

To evaluate unmerged rules, explicitly select their commit as the policy revision
in the prompt. Reviewing a PR that introduces rules does not select those rules
as policy. Rebuilding the bundled skills updates the investigation methods;
repository rules still come from the selected trusted revision. The review
artifact names its policy files, including when it used only `AGENTS.md`.

## Results and publication

Ordinary reviews keep concise investigation notes, findings, checks, and material
gaps. Use the bundled [result contract](../internal/images/context/agent/env/skills/review-pr/references/results.md)
for delegation, evaluations, or requested saved/machine-readable results.
Missing applicable investigations produce a partial review. Check output stays
separate from semantic findings; skipped optional integrations remain visible.
Use `PACT_CI_BASE` for the actual PR delta when running Pact's checks, especially
on stacked PRs. A complete review can have no findings.

Only the coordinator publishes, following the
[publication procedure](../internal/images/context/agent/env/skills/review-pr/references/publishing.md).
It checks existing discussions, uses stable finding markers across pushes, and
rechecks revisions before posting. A review is advisory unless an explicit task
requests an approval or change-request decision. With the current Forgejo MCP
review tool, findings go in a SHA-labeled summary with permanent source links:
the tool does not expose commit-bound inline posting. A connector supporting
explicit commit binding can use inline comments. Uncertain writes are reconciled
by reading the forge before retrying.

A requested publication includes a summary even with no semantic findings or a
failed check. A no-new-findings summary is one short paragraph of one or two
sentences, with only material check failures or validation gaps. Policy details,
coverage inventories, and routine check results stay in the local artifact.
The final response links the posted or reused review without repeating it, or
states the publication failure or revision change that prevented posting.
Saving a local JSON result alone does not complete a request to publish.

This is an agent workflow, not a deterministic enforcement mechanism.
Investigation notes record what was examined; they do not prove model compliance
or exhaustive correctness. Event-triggered session startup and a CI review gate
are not installed by these skills. The same review workflow can be invoked
by future automation.

## Evaluating changes

Use fresh sessions and the same source/policy revisions when comparing changes.
Replay actual historical review snapshots where available, then their corrected
revisions. Preserve the original PR base/head: the live base field of a closed
PR may now name a later target-branch commit. Old pre-amend commits can disappear
from the forge; record unavailable cases rather than treating merged code as
the original failing snapshot.

Useful existing cases are PR 28 for test behavior and README audience, PR 88 for
concrete APIs, PR 104 for approval parsing and cancellation, and PR 124 for source
scanner behavior. Test corrected revisions and no-finding outcomes as well as
detection. Judge findings by current evidence and their treatment of relevant
context, rather than using prior acceptance or rejection as the sole oracle.
These are seeded examples, not held-out accuracy measurements. Reserve other
PRs before tuning patterns for a broader eval.

For an effectiveness study, compare fresh reviews with and without the skills
on the same unseen Pact PR snapshots and unrelated repositories. Track useful
findings, false positives, missed bugs, time/token cost, and whether Pact-specific
preferences leak into other projects.

Recorded snapshots for reproducible dry runs are:

| PR | Diff base | Reviewed head |
| --- | --- | --- |
| 88, original | `43ab11ff7ef95b53ef22a16f36d849627a3c9aff` | `2e953f582dccc28fcf3620437ebabb3cd029be46` |
| 88, corrected | `43ab11ff7ef95b53ef22a16f36d849627a3c9aff` | `29ac91d9ec510eaeb057f2b749414a7d708744d8` |
| 104, original | `0c7851ad36e9459736876c87522e5dce087cddb2` | `c35fe5469c6b1fb4bb8a4d3bdd439405aa373775` |
| 104, corrected | `2c18e096f3c69ffe48ca1d7e03ae43674108fdb2` | `2c344596924b1abae259d7060fce593580d97052` |
| 107, original | `ca5157c1c148e4efcf9766fe98cfd44ac1fe333e` | `fa2403851d8d4b6c8a51f196b88da5a3d21ec0a3` |
| 107, corrected | `ca5157c1c148e4efcf9766fe98cfd44ac1fe333e` | `6b2d3b1565e29b4031eeb2c76dfe8048c8618426` |
| 124, corrected | `1b8859e196924ed7b6faf425122b30c352b336a9` | `9ca9d355c6cee0ad268a761e3b1d8d538373174a` |

When local execution is prohibited for an evaluation, required checks remain
notRun and the review is partial unless the task explicitly accepts equivalent
CI evidence. Record remote and local validation separately.

Run evaluation tasks without publishing. Give independent reviewers the skill,
raw PR/source artifacts, and task scope without the expected findings. Check
missed patterns, unsupported findings, duplicate reports, use of prior context,
and repository scoping. Missing required evidence should produce partial status.
Validate the bundled Nix package and normal `./shell p-ci` separately; passing
those checks does not demonstrate review quality.
