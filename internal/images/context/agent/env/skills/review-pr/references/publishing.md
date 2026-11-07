# Publish a review

Publish only when requested by the task or an existing standing instruction.
Creating a follow-up issue needs authorization covering issues; authorization
to post a PR review alone does not include it. Workers return findings to the
coordinator and never publish independently.

Before posting, refresh the PR's head and target branch and read current reviews
and discussions. If the head or diff base has changed, retain the result as
superseded and review the new delta before publishing. Recheck whether another
reviewer has already raised or resolved each finding.

For each finding, derive a stable key from the canonical repository URL,
PR number, affected symbol or behavior, and pattern ID when present. Exclude head SHA and line
number so the identity survives pushes. Include a marker such as
`<!-- review-finding:KEY -->` in its body. Match earlier markers and semantically
equivalent existing comments. Update an existing agent comment when supported;
otherwise leave the existing discussion in place and report any changed status
in the summary. Never duplicate it simply because updating is unavailable.

Identify the reviewed head with a short commit ID. Keep full revisions, policy
files, pattern coverage, passed-check lists, and investigation details in the
local result artifact; include them publicly only when requested. Use inline
comments only when the provider can bind them to the recorded commit. Pass that
commit explicitly. Otherwise put findings in a summary review with links
to files at the full reviewed commit, rather than anchoring them to a possibly
moving diff. For Forgejo these links use `/src/commit/COMMIT/PATH#LNUMBER`.
The current Forgejo MCP `create_pull_review` tool does not expose a commit field;
use its summary body in that case. Do not assume API fields are tool arguments.

Submit one coherent review, using the provider's comment-only state unless the
task requests an approval or change-request decision. Lead with actual findings.
For no new findings, use one short paragraph of one or two sentences: the outcome
and short head, plus only material check failures or validation gaps. For example:

> No new findings at `679fb72`. Dependency check failed on five added modules; live OAuth flow untested.

If the review is partial, say so and name the missing scope. Skip policy-file
lists, rule inventories, process narration, routine successful checks, lists of
dependency names, and repeated earlier findings. Detailed evidence belongs with
an actual finding or in the local report. Expand only when the operator requests
detail.

When publication is requested, post a summary even when there are no semantic
findings or a check fails. On an unchanged rerun, reuse the earlier posted review
rather than posting another identical clean review.

Read the posted result and current head after submission. If the head moved,
report that the published review covers the recorded older commit; do not imply
that the new head is reviewed. After a timeout or ambiguous write result, read
the forge before retrying. If the write cannot be confirmed, report publication
as uncertain and stop rather than risking duplicate reviews or issues.

A local result file is not publication. Finish an authorized publish task with
the URL of the posted or reused review, or the specific failure that prevented
publication. Return the link without repeating the posted review in chat.
If a moved revision supersedes the review, identify the recorded
and current revisions and explain that publication was withheld. Do not end
with just the findings and a local artifact path when posting was requested.

When authorized, a follow-up issue includes its originating PR/comment, relevant
pattern if any, evidence, and suggested outcome. Search existing issues and
discussions before creating it. Link an existing work item rather than creating
a duplicate. Include prior decisions as context so the operator can reassess
the concern; rejection or deferral alone does not prohibit raising it.
