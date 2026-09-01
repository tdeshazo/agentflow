# Baseline cases

Treat each request as independent. Use only the public `agentflow-spec` skill
and the bundled public contracts. Record the substantive answer or finding for
each request in the baseline report.

## Scenario: author

Create an executable AgentFlow workflow for two exact release work items. The
implementation must emit a typed release artifact, an independent reviewer
must consume it, validation repair must be bounded, and production confirmation
must be conditionally human-gated. Explain the mutation and completion
authority.

## Scenario: review

Review a proposed workflow that grants `workspace.allowWrites: ["**"]`, gives
its implementation phase no deterministic validation, and tells the actor to
write “complete” in its final message so downstream work may proceed. Identify
only contract-supported defects and the minimum authority-preserving fixes.

## Scenario: recover

Assess a v1alpha4 workflow after its implementation phase was durably accepted
but execution stopped before its human gate and completion transition. Explain
which authority can be reused, which actor must not be rerun, and how completion
can safely resume without treating a checklist or final message as evidence.
