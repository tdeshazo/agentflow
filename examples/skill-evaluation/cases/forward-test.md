# Independent forward-test cases

Treat each request as independent. Do not consult the baseline report before
scoring the revised skill. Record the substantive answer or finding for each
request, then issue the required verdict.

## Scenario: author

Create an executable AgentFlow release workflow with two stable work-item IDs,
a typed artifact passed to an independent audit, exactly one repair attempt for
the implementation gate, a hard post-audit boundary, and a conditional human
acknowledgement. Keep engine-owned progress distinct from Markdown
presentation.

## Scenario: review

Review a workflow whose “independent” final audit uses a repair actor that also
authored the accepted implementation. The repaired workspace goes directly to
completion without another independent review. Explain the authorship and
acceptance problem and give a bounded correction.

## Scenario: work-items

Design v1alpha4 work-item execution for three statically declared components.
Use bounded `forEach`, exact runtime-owned completion, disjoint phase write
scopes, and an optional Markdown checklist mirror. Explain why actor prose and
checklist edits cannot advance work.
