---
name: review-tests
description: Assess what failures changed tests protect against during a requested review, using repository guidance, learned patterns, and current evidence.
---

# Review tests

Read the assignment's scope, revisions, and any repository testing guidance.
Without supplied rules or patterns, derive questions from the changed production
behavior and test assertions; no rule IDs are required. Policies on test doubles,
integration scope, and test location belong to that repository. An unspecified
policy does not make a test double or test location a violation by itself.

For each relevant changed test, identify the concrete application failure it
would catch. Read the production path and existing tests. Trace the stimulus,
real operation or substitute, and observation. Explain whether the observation
protects the external behavior or merely repeats how the code is written.
A test's filename or location alone cannot establish its usefulness.

Compare overlapping tests by failure mode. Distinct layers can protect distinct
contracts; superficial similarity is not evidence of duplication. Conversely,
different test names can protect the same implementation detail. Assess a
missing-test claim against a specific changed failure mode and available guidance.

Recognize justified executable-boundary tests and integration tests under project
policy. Check what a test double replaces before judging it. Replacing Docker
with a script on PATH is still a double, even when the test uses a real process.
Do not insist on an expensive integration test where removing an unnecessary
test is the appropriate project-specific outcome.

Return candidates and concise investigation evidence with test and production
locations, the failure protected or missed, and a focused requested outcome.
Group unaffected patterns by concern rather than listing each separately.
Attach a learned pattern and source when applicable; other relevant test concerns
can be raised on their own evidence.
Follow the coordinator's result contract. Do not edit code, run shared builds,
change checkouts, or publish. Ask the coordinator for necessary executions.
