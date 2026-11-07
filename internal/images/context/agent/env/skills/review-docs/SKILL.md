---
name: review-docs
description: Reconcile changed documentation claims with implementation, identify the reader action a paragraph supports, and check protocol examples using repository guidance when available.
---

# Review documentation

Read the assigned revisions, patterns, project guidance, and target claims.
State the question about this delta, such as "Which operator action
does this new README paragraph support?" The repository supplies the audience
and documentation requirements. Patterns guide the investigation; other relevant
documentation concerns can be raised on their own evidence.

Without a project catalogue or assigned rules, derive questions from the changed
claims and behavior. Infer the audience from existing document usage and state
uncertain assumptions; do not import another repository's README policy. No
pattern IDs are needed.

## Investigations

Choose the investigation matching the question and changed behavior. These
methods do not create new documentation obligations.

| Changed shape | Work to do | Evidence to return |
| --- | --- | --- |
| CLI behavior, configuration, default, or user-visible limitation | Establish the before/after contract from the parser and behavior path. Search docs for affected commands, field names, or claims, even if no doc file changed. Compare those claims with the new behavior. | Changed behavior → affected claim/example → implementing symbol, with locations and whether they agree. |
| Paragraph in a document with an audience rule | Read the paragraph in context. Identify its reader, the action or decision it enables, and the relevant policy. For an internal explanation, look for the established internal document that owns the topic. | The paragraph, supported reader action, and placement rationale. |
| Protocol, payload, or data example | Trace the producer/consumer and encoding/decoding code. Compare the example's concrete fields and values with that path. If an example is missing, identify the specific ambiguity one would resolve. | Producer/consumer → payload/example comparison, including a small data shape when useful. |

An internal term can still explain an operator-relevant limitation. A code change
can leave the public contract unchanged. Establish that distinction from the
changed behavior rather than demanding a doc edit for every change. Treat an
explicit proposal as a proposal rather than implemented behavior.

## Result

For actual investigations, give the question, key locations, and answer concisely,
even when there is no finding. Group unaffected patterns by concern. A clean
result names the claim and supporting source, or the reader action justifying the
paragraph. Keep the comparison concise rather than listing every file inspected.
"Docs match the code" is insufficient evidence. If there is no
applicable contract, paragraph, or payload change, state that basis.

A finding needs a source location, current evidence of a mismatch or audience
problem, and a focused suggestion. Cite a learned pattern and source when
applicable; other findings do not need one. Check relevant links
and use official versioned sources when an external API claim affects that
conclusion. Follow the coordinator's result contract. Do not edit files, change
checkouts, run shared builds, or publish. Request an execution from the
coordinator if an example cannot be assessed by inspection.
