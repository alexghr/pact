# Review results

For an ordinary review, keep concise notes on actual investigations, findings,
checks, and material gaps. Detailed structured reporting is for delegation,
evaluations, or requested saved/machine-readable results. When producing an
artifact, keep it outside the source checkout and use camelCase JSON with the
shape below. Revisions stand for full commit IDs; the example is not a finding
to reproduce.

This is the working record for coverage, provenance, and validation. Do not
serialize it into the PR comment or repeat it in the final chat reply. Published
reviews follow the much shorter format in [publishing](publishing.md).

```json
{
  "repository": "https://forge.example.com/owner/repo",
  "connection": "configured-mcp-server-name",
  "pr": 125,
  "base": "full diff-base commit",
  "head": "full reviewed commit",
  "policyRevision": "full policy commit",
  "status": "complete",
  "coverage": [
    {
      "ruleId": null,
      "status": "reviewed",
      "reason": "Does the test protect behavior? file_test.go:TestRun calls file.go:Run; assertions repeat command arguments."
    }
  ],
  "checks": [
    {"command": "project check command", "status": "passed", "reason": ""}
  ],
  "findings": [
    {
      "ruleId": null,
      "source": null,
      "path": "relative/source/file",
      "line": 42,
      "anchor": "symbol or behavior being reviewed",
      "title": "Concrete problem",
      "evidence": "What the changed code demonstrably does",
      "impact": "Concrete consequence or reason to address the concern",
      "requestedChange": "Focused outcome for the implementer"
    }
  ],
  "limitations": []
}
```

Use the canonical repository URL returned by the forge as repository identity,
so equal names on different servers remain distinct. Keep the configured
connection name separately for tool routing; do not substitute a display alias
such as `forgejo` for the repository URL. Workers echo the coordinator's exact
repository, connection, PR, base, head, and policy identity, plus their concern
and assigned rule coverage. The coordinator aggregates one result per PR.

Workers return investigation coverage and candidate findings. Project checks
belong to the coordinator; workers do not mark completed investigations partial
because they did not execute those checks. The coordinator sets the overall
review status from combined coverage and check evidence.

Review status is `complete` or `partial`. Coverage status is `reviewed`,
`notApplicable`, or `skipped`. Record actual investigations, including those with
no findings, and material skipped scope. Group unaffected patterns in a single
notApplicable entry by concern, with a short reason; set `ruleId` to null for
groups or questions with no pattern ID. Enumerate every pattern only when an
evaluation or operator explicitly requests that level of coverage. An
investigation that could not establish applicability is skipped, not
notApplicable. Check status is `passed`, `failed`, or `notRun`, with a reason
when not passed.
For reviewed investigations, the reason records the question, key locations, and
answer. Include the relevant caller/owner trace or claim/implementation comparison
requested by the concern skill. A clean conclusion needs supporting evidence
too; "reviewed design" or "docs look correct" is not sufficient coverage.
Use the shortest cited explanation that establishes the result, without copying
raw exploration notes or repeating the same evidence in multiple fields.
Skipped applicable investigations or missing required policy/context make the
review partial. Optional integration checks can be notRun without making the
review partial; retain their limitations. A failed check is evidence of a check
failure, not automatically a failed semantic investigation.

A required check marked notRun makes the review partial. A task that prohibits
local execution does not by itself waive required checks. Substitute equivalent
CI evidence only when the task or repository policy explicitly accepts that
substitution, and verify that it covers the reviewed revision and required
baseline. Record the evidence URL and distinguish remote success from commands
actually executed locally.

Every finding needs a location in the reviewed revision, current evidence,
impact, and a suggested outcome. A learned pattern is useful provenance, not
an eligibility requirement. Set `ruleId` to its ID when applicable, otherwise
null; use `source` for a relevant pattern or supporting reference, otherwise null.
Do not invent a rule to fit an independent finding. Apply the project's severity
convention if one exists, and distinguish observed behavior from assumptions
or uncertain concerns. Prior rejection or deferral belongs in the context,
not in an automatic suppression rule. The operator chooses what to implement.
