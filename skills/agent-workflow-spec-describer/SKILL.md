---
name: agent-workflow-spec-describer
description: Efficiently explain, summarize, review, or compare AgentWorkflow YAML specifications (agentflow.dev/v1alpha1), including agents, deterministic tools, workspace mutation boundaries, validation, recovery, human gates, control flow, and completion. Use when a user asks what an AgentWorkflow does, how it executes, what can mutate, how failures recover, what humans must verify, or whether a workflow preserves the intended orchestration semantics.
---

# Agent Workflow Specification Describer

Explain an `agentflow.dev/v1alpha1` `AgentWorkflow` from the workflow definition itself. Treat the YAML as the source of truth. Do not infer capabilities, mutation authority, retries, or completion behavior that are not declared.

## Goal

Turn a potentially long workflow into a compact operational description that answers, in order:

1. What outcome does the workflow own?
2. What is allowed to change, and what is protected?
3. Which agents and deterministic tools participate?
4. What is the execution sequence?
5. What deterministic conditions permit advancement?
6. What happens on failure or interruption?
7. What requires human verification?
8. What makes the workflow complete?

Prefer control semantics over repeating agent prompts. Summarize prompt details only when they materially constrain implementation.

## Fast path

For a normal description request, read these sections first:

- `metadata`
- `spec.parameters`
- `spec.workspace`
- `spec.agents`
- `spec.validation`
- `spec.phaseDefaults`
- `spec.phases`
- `spec.humanGates`
- `spec.recovery`
- `spec.flow`
- `spec.completion`

Read `spec.state`, `spec.preconditions`, `spec.progress`, and `spec.tools` when the user asks about resumability, safety boundaries, acceptance criteria, deterministic behavior, or implementation details.

Use `references/agentflow-v1alpha1.md` only when a field's meaning is unclear or a detailed specification review is requested.

## Description algorithm

### 1. Identify the workflow contract

Extract:

- workflow name and stated purpose;
- source artifact, if declared;
- runtime parameters that materially alter behavior;
- the success boundary named under `completion`.

Describe the workflow as an orchestrator, not as a collection of prompts.

### 2. Separate authority domains

Always distinguish these three authorities:

- **Agent authority** — what AI actors may attempt inside their sandbox.
- **Workspace authority** — which paths may mutate and which content is integrity-protected.
- **Validation authority** — deterministic checks that decide whether execution advances.

If agents may commit, state that this does not imply they may decide phase acceptance.

### 3. Compress workspace safety rules

Summarize `workspace` into no more than four ideas unless more detail is requested:

- clean-workspace requirements;
- allowed mutation scope;
- protected/integrity-checked content;
- checkpoint/commit behavior.

Call out `normalized-hash` rules because they permit only specific semantic bookkeeping changes while freezing surrounding text.

### 4. Summarize actors and tools by role

Do not list every runner option unless asked. Prefer:

- actor → model family / reasoning level when phase-specific;
- deterministic gate → authoritative pass/fail command;
- scope/integrity tools → mutation enforcement;
- checkpoint tool → durable Git state.

### 5. Describe phases as a sequence of intent

For each phase, capture only:

- `id`;
- `kind`;
- `label`;
- actor and reasoning;
- criterion, when present;
- whether repository change is required;
- one-sentence intent distilled from the prompt.

Do not reproduce long prompts unless the user asks for them.

Group adjacent phases when that improves clarity, for example:

- criterion implementation phases;
- integration/audit phases;
- completion bookkeeping.

### 6. Explain advancement deterministically

State the phase acceptance pipeline in execution order. Include inherited `phaseDefaults` plus phase-specific overrides.

Typical form:

`agent run → scope check → canonical gate → bounded repair if configured → progress invariant → checkpoint → net-change assertion → phase marker`

Never say a phase succeeds merely because the agent returns successfully.

For criterion phases, explain `unchecked_count_delta`, targeted-item checks, or equivalent progress invariants.

### 7. Explain failure policy precisely

Different validation gates may have different policies. Describe each independently.

Distinguish:

- hard failure with no repair;
- one-shot repair;
- bounded retries;
- resume-time validation;
- rerunning the original phase.

Do not generalize one gate's retry policy to all gates.

### 8. Explain resumability as state transitions

When `state.resume` or `recovery` exists, explain:

- what persistent records are stored;
- what makes a completed phase marker valid;
- how an active interrupted phase is recognized;
- whether existing partial commits/worktree changes are preserved;
- whether current state is validated before rerunning an agent;
- how recovered work is checkpointed and marked complete.

Use the word **idempotent** only when completed-state markers and recovery rules actually support safe repeated invocation.

### 9. Treat human gates as first-class

For each human gate, state:

- when it occurs;
- whether it can be skipped;
- what environment/behavior the human verifies;
- required acknowledgement;
- durable evidence recorded.

Summarize long checklists by verification themes unless the user explicitly asks for every item.

### 10. Explain completion separately from implementation

Completion is not implied by all implementation phases finishing.

Describe:

- completion assertions;
- final validation;
- final checkpoint;
- post-checkpoint integrity/cleanliness checks;
- completion marker;
- emitted summary/evidence.

## Default response format

Use this compact structure unless the user requests another format:

### Purpose
Two or three sentences describing the owned outcome and orchestration model.

### Safety and authority
A short paragraph covering mutation scope, protected content, clean-state rules, and who owns acceptance.

### Execution
A compact table with columns:

`Phase | Kind | Actor | Effort | Intent | Change required`

Follow it with the deterministic phase acceptance pipeline.

### Failure and recovery
Explain gate-specific repair behavior and interrupted-phase recovery.

### Human verification
Summarize manual checks, skip behavior, acknowledgement, and recorded evidence.

### Completion
State the exact conditions required before the durable workflow-complete marker is written.

## Ultra-compact mode

If the user asks for a brief description, produce exactly five bullets:

1. Outcome.
2. Mutation/protection boundary.
3. Phase sequence and actors.
4. Validation/recovery policy.
5. Human/completion boundary.

## Detailed review mode

If the user asks to review, audit, validate, or critique the specification, additionally check for:

- a mutable path that bypasses `mutationPolicy.allowed`;
- protected content that is not asserted after mutation;
- an agent-controlled success condition without deterministic validation;
- repair policies that accidentally apply to hard gates;
- criterion phases lacking progress invariants;
- phase completion markers not tied to Git lineage;
- resume behavior that discards or blindly trusts partial work;
- human gates without durable evidence;
- bookkeeping before required human verification;
- completion markers written before final validation/checkpoint/integrity checks;
- `requiresChange: false` phases that are incorrectly rejected for producing no diff;
- skip paths that can mark a criterion phase complete without sufficient validation.

Report only issues supported by the YAML. Label inferred risks as inferences.

## Comparison mode

When comparing two workflows, compare semantics rather than YAML text. Use these dimensions:

- state/resume model;
- mutation scope and integrity rules;
- agent/model allocation;
- phase granularity;
- deterministic gates;
- repair budget;
- progress invariants;
- checkpoint strategy;
- human verification;
- completion contract.

Call out semantic differences even when the field layout differs.

## Efficiency rules

- Read control fields before prompts.
- Do not restate defaults repeatedly for every phase.
- Distill prompts to one sentence per phase.
- Expand only the section relevant to the user's question.
- Prefer execution order over YAML declaration order when explaining runtime behavior.
- Mention exact paths, regexes, commands, or checklist items only when they matter to the question.
- Preserve declared terminology such as `phaseGate`, `canonical-gate`, `normalized-hash`, `active_phase`, and `workflow_complete` when precision matters.
- Do not invent engine behavior for undeclared `type` values; describe their intended role from local usage and flag undefined runtime semantics if necessary.

## Source-of-truth rule

When the workflow was derived from another artifact (for example a shell orchestrator), describe the YAML specification as written unless the user asks for equivalence verification. For equivalence verification, compare the YAML against the source artifact and explicitly separate:

- faithfully represented behavior;
- behavior simplified by the DSL;
- behavior omitted or changed;
- behavior whose runtime semantics depend on the workflow engine.
