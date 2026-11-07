---
name: review-design
description: Review changed behavior and design by tracing callers, state transitions, ownership, and contracts, using repository guidance when available.
---

# Review design and boundaries

Read the assigned revisions, patterns, project guidance, and target operations.
If no patterns or rule IDs are supplied, derive concrete questions from the diff,
callers, and intended behavior. State assumptions where intent is unclear;
project guidance helps the investigation but is not a prerequisite.
Turn each applicable pattern into a question about this delta. For
example: "After this extraction, does the caller still construct the operation's
command?" Use the repository's named owners and accepted examples to choose
where to look. Also investigate other relevant concerns encountered in the
changed operation; a finding does not need to match a learned pattern.

## Investigations

Choose the investigation matching the question and changed operation. These are
methods, not additional rules or a requirement to run every investigation.

| Changed shape | Work to do | Evidence to return |
| --- | --- | --- |
| Behavior or state transition | Compare intended behavior with the changed path for normal inputs and meaningful boundary/error cases. Follow outputs, state changes, and effects on callers. | Expected versus actual behavior for a concrete input or transition, with source locations. |
| New API, wrapper, or moved logic | Read changed callers and follow arguments through the exposed API to the actual operation. Identify which side chooses behavior, constructs low-level inputs, and owns cleanup. | A caller → API → operation trace with locations and the responsibility retained at each step. |
| Another transport, resource setup, or helper | Search the repository for the same operation. Compare the closest existing implementation, including lifetime, cancellation, and error handling. | Both implementations and the shared responsibility or material difference. |
| New guard, validation, or matching logic | Trace where the value comes from, the accepted contract, and what happens after the check. Identify a concrete valid or invalid input affected by the new logic. | The guard, contract source, input, and acceptance/rejection path. |
| A decision made before forwarding or execution | Follow the same input through interpretation, decision, and execution. Compare the selected values and transformations at each stage. | An input → decision → executed-action trace; include a payload when it distinguishes the interpretations. |

Start from the changed symbols and expand only far enough to answer the question.
An import list cannot establish semantic ownership; similarly named helpers
cannot establish equivalent behavior. When inspecting matching logic, trace a
specific match or miss through the branches rather than judging syntax alone.

## Result

For actual investigations, give the question, key locations, and answer concisely,
even when there is no finding. Group unaffected patterns rather than reporting
each one separately. A clean ownership result identifies what the caller
supplies and what the owner builds.
Use the shortest cited trace that supports the answer; keep exploration notes
and inventories out of the returned coverage.
"Package boundaries look good" is insufficient evidence. If no matching change
exists, a brief concern-level explanation is enough. Rule IDs are optional.

A finding needs a source location, current evidence, its consequence, and a
focused suggestion. Cite a pattern and its source when one informed it; otherwise
explain the concern on its own merits. Link relevant prior discussion, including
rejections or deferrals, without treating those as a ban on raising it. Check
the actual contract: removing validation can violate another boundary, and sharing
similar code can hide different lifetimes. Follow the coordinator's result
contract. Do not edit code, change checkouts, run shared builds, or publish.
Request a reproduction when source inspection cannot establish the answer.
