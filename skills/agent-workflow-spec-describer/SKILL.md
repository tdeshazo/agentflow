---
name: agent-workflow-spec-describer
description: Create, modify, explain, summarize, review, validate, or compare AgentFlow AgentWorkflow YAML specifications (agentflow.dev/v1alpha1), including agents, deterministic tools, workspace mutation boundaries, validation, recovery, human gates, control flow, and completion. Use when a user asks to author a workflow, turn a task or process into AgentFlow YAML, change an existing workflow, explain what a workflow does, review its safety or resumability, compare specifications, or diagnose a validation/design problem.
---

# Agent Workflow Specification

Work with `agentflow.dev/v1alpha1` `AgentWorkflow` documents from the public workflow contract. The skill must be sufficient to author executable workflows without reading AgentFlow implementation source.

## Source boundary

For normal workflow authoring, modification, description, or review:

- Treat the workflow YAML plus this skill's bundled references as the specification surface.
- Do **not** inspect `internal/`, `provider/`, tests, or other AgentFlow implementation source to discover fields or runtime behavior.
- Use `references/authoring-agentflow-v1alpha1.md` for authoring patterns, supported runtime values, canonical templates, and the validation loop.
- Use `references/agentflow-v1alpha1.md` for field semantics and detailed review questions.
- If the CLI rejects a construct documented by these references, report specification/documentation drift rather than reverse-engineering implementation source.
- Inspect AgentFlow source only when the user explicitly asks to debug or change the AgentFlow implementation itself.

Never invent fields, tool types, expression forms, phase kinds, or acceptance semantics that are not documented by the bundled references.

## Modes

Choose the mode from the user's request.

### Authoring mode

Use when creating a new workflow or modifying an existing workflow.

Primary goal: produce valid, executable YAML whose control and acceptance semantics are explicit. An agent prompt may direct work, but deterministic workflow logic must decide advancement.

Read `references/authoring-agentflow-v1alpha1.md` first. Read the field guide only for semantics that need expansion.

### Description mode

Use when explaining what a workflow does. Treat the YAML as the source of truth. Do not infer capabilities, mutation authority, retries, or completion behavior that are not declared or supplied by documented defaults.

### Review mode

Use when auditing or validating a workflow design. Check schema/reference integrity, authority boundaries, deterministic acceptance, recovery, human evidence, and completion ordering.

### Comparison mode

Use when comparing two workflows. Compare semantics rather than YAML text.

## Authoring algorithm

### 1. Define the owned outcome

Before writing YAML, identify:

- the bounded outcome;
- the repository/workspace the workflow operates on;
- which paths may change;
- which paths or structures must remain protected;
- what deterministic command or check proves acceptance;
- whether progress is a checklist, ordinary implementation, or both;
- whether a human must verify anything;
- what durable condition means the workflow is complete.

If the user did not specify every detail, choose the smallest safe design that satisfies the request. Do not add complex state, recovery, human gates, or dynamic loops unless they provide concrete value.

### 2. Choose the simplest executable shape

Prefer these shapes in order:

1. **Single implementation phase** — one bounded change with one deterministic gate.
2. **Implementation + audit** — implementation followed by an independent audit/repair phase.
3. **Criterion workflow** — explicit checklist criteria with `criterionID` and `advanceProgress: true`.
4. **Human-gated workflow** — add `humanGates` only for verification a deterministic tool cannot establish.
5. **Bookkeeping/completion workflow** — add engine-owned Markdown bookkeeping only for exact structured transitions.
6. **Dynamic criterion loop** — use only when a declared checklist drives repeated phase dispatch.

Prefer `spec.defaults` plus `spec.lifecycle.policy: safe-resume` for new workflows. Avoid legacy procedural `phaseDefaults`, phase `after`, and explicit `recovery` unless modifying an existing legacy document that already depends on them.

### 3. Establish authority boundaries

Keep these domains separate:

- **Agent authority** — named actors, runner/model/sandbox/approval, and prompts.
- **Workspace authority** — `workspace.mutationPolicy.allowed`, integrity rules, Git lineage, and cleanliness.
- **Validation authority** — deterministic tools and named `validation` gates.
- **Human authority** — explicit `humanGates` with acknowledgement and durable evidence.
- **Completion authority** — assertions, final validation, checkpointing, and the complete marker.

Agent success is never phase acceptance. Every mutable AI phase must resolve to a deterministic validation gate through the phase, phase-kind defaults, or lifecycle.

### 4. Author in dependency order

Write sections in this order so references exist before they are used:

1. `apiVersion`, `kind`, `metadata`;
2. `spec.parameters` and `spec.paths`;
3. `spec.workspace` and optional state/reset policy;
4. `spec.defaults` and `spec.agents`;
5. `spec.tools`;
6. `spec.preconditions`;
7. `spec.validation`;
8. `spec.progress` when criteria are used;
9. `spec.phases`;
10. `spec.humanGates` when needed;
11. `spec.completion` when durable completion is needed;
12. `spec.flow`.

Use stable IDs for agents, tools, validations, phases, criteria, gates, and completion contracts.

### 5. Make prompts bounded

A phase prompt should say:

- exactly what outcome or criterion to work on;
- the scope boundary;
- relevant deterministic checks to run locally;
- what not to change;
- whether a diff is required.

Do not put acceptance authority in the prompt. Avoid instructions such as "mark this phase complete" when `advanceProgress` or completion logic is engine-owned.

### 6. Design deterministic validation

Every validation must contain at least one deterministic `steps` entry.

Prefer one canonical repository gate where possible. Use `dependencies` to state the files/globs the gate reads when known.

Failure policy should be explicit:

- hard gate: `repair: none` or no repair settings;
- bounded repair: `repair: once` with a declared/default repair actor;
- never use unbounded retries.

A repair actor must repair the bounded failure and the deterministic gate must run again. Repair success alone is not acceptance.

### 7. Add progress only when it is real state

For checklist-driven workflows:

- declare `progress.criteria` with stable `id` and exact `text`;
- target criteria with `phase.criterionID`;
- prefer `advanceProgress: true` so the engine owns the checkbox transition;
- use `first-unchecked` selection only when document order is intended to drive execution;
- add a negative progress delta invariant when each phase must close exactly one item.

Do not ask an actor to edit a progress checklist that the engine is configured to advance.

### 8. Add human gates only for human evidence

A human gate should identify:

- placement/dependencies;
- the exact verification procedure;
- a short checklist;
- acknowledgement protocol;
- durable evidence at the accepted commit;
- an explicit conditional/skip path only when bypass is intended.

Do not use a human gate as a substitute for a deterministic test that can run automatically.

### 9. Make completion a separate transition

For durable completion, prefer:

`assertions → finalValidation → checkpoint → afterCheckpointAssertions → writeMarker → summary`

Write the complete marker last. A flow finishing its last phase is not automatically the same as a durable completion contract.

### 10. Validate before presenting or running

When the AgentFlow CLI is available, author iteratively:

```sh
agentflow validate -f workflow.yaml
agentflow plan --expanded -f workflow.yaml
```

Inside this repository, the equivalent development commands are:

```sh
go run . validate -f workflow.yaml
go run . plan --expanded -f workflow.yaml
```

Fix every `invalid` diagnostic. If the document is valid but `unsupported`, remove or replace the unsupported runtime construct unless the user explicitly wants a descriptive/non-executable specification.

Inspect the expanded plan for resolved:

- agent capabilities;
- lifecycle and phase validation;
- repair actor and retry budget;
- mutation/progress behavior;
- checkpoint behavior;
- human gates;
- completion ordering.

Do not run a workflow merely to learn whether its YAML is valid.

## Authoring output expectations

When asked to create a workflow:

- Return or write one complete `.agent-workflow.yaml` document unless the user asks for fragments.
- Use the current `agentflow.dev/v1alpha1` / `AgentWorkflow` identifiers.
- Prefer concise defaults and runtime-owned safe resume.
- Include comments only where they explain a non-obvious safety or authority decision.
- Keep prompts shorter than orchestration logic.
- If repository facts are unavailable, parameterize them rather than fabricating file paths or commands.
- State any assumptions that materially affect mutation scope, validation, human verification, or completion.

When asked to modify a workflow, preserve existing semantics outside the requested change and rerun the validation loop.

## Description algorithm

For a normal description request, read these sections first:

- `metadata`
- `spec.parameters`
- `spec.workspace`
- `spec.agents`
- `spec.validation`
- `spec.defaults` / `spec.lifecycle` or legacy `spec.phaseDefaults`
- `spec.phases`
- `spec.humanGates`
- `spec.recovery`
- `spec.flow`
- `spec.completion`

Read `spec.state`, `spec.preconditions`, `spec.progress`, and `spec.tools` when the user asks about resumability, safety boundaries, acceptance criteria, deterministic behavior, or implementation details.

Explain the workflow in this order:

1. owned outcome and completion boundary;
2. mutation and integrity boundary;
3. actors and deterministic tools;
4. execution order;
5. deterministic advancement conditions;
6. failure/repair behavior;
7. interruption/recovery behavior;
8. human verification;
9. terminal completion.

Prefer control semantics over repeating agent prompts.

### Authority summary

Always distinguish:

- **Agent authority** — what AI actors may attempt.
- **Workspace authority** — what can mutate and what is protected.
- **Validation authority** — deterministic checks that decide advancement.

If agents may commit, state that this does not imply they decide phase acceptance.

### Phase summary

For each phase, capture only:

- `id`;
- `kind`;
- label;
- actor and reasoning;
- criterion when present;
- whether repository change is required;
- one-sentence intent.

Group adjacent criterion or audit phases when that improves clarity.

### Advancement summary

Describe the resolved acceptance pipeline in execution order. For safe-resume workflows this is generally:

`agent run → scope/integrity checks → deterministic gate → bounded repair if configured → progress/net-change assertions → checkpoint → completed-phase evidence`

Never say a phase succeeds merely because the agent returns successfully.

### Failure and recovery

Distinguish:

- hard failure with no repair;
- one-shot repair;
- exhausted repair budget;
- safety/integrity failure;
- resume-time validation;
- actor replay versus acceptance replay after interruption.

Use **idempotent** only when durable markers and recovery rules support safe repeated invocation.

### Human verification

For each human gate, state:

- when it occurs;
- whether it can be skipped;
- what the human verifies;
- required acknowledgement;
- durable evidence recorded.

### Completion

Describe completion separately from implementation. State the assertions, final validation, checkpoint, post-checkpoint checks, complete marker, and summary/evidence.

## Default description format

Use this structure unless the user requests another format:

### Purpose

Two or three sentences describing the owned outcome and orchestration model.

### Safety and authority

A short paragraph covering mutation scope, protected content, clean-state rules, and who owns acceptance.

### Execution

A compact table:

`Phase | Kind | Actor | Effort | Intent | Change required`

Follow it with the deterministic phase acceptance pipeline.

### Failure and recovery

Explain gate-specific repair behavior and interrupted-phase recovery.

### Human verification

Summarize manual checks, skip behavior, acknowledgement, and recorded evidence.

### Completion

State the exact conditions required before durable workflow completion.

## Ultra-compact description mode

If the user asks for a brief description, produce exactly five bullets:

1. Outcome.
2. Mutation/protection boundary.
3. Phase sequence and actors.
4. Validation/recovery policy.
5. Human/completion boundary.

## Detailed review mode

Check for:

- unknown or undocumented fields/types;
- unresolved or dangling named references;
- mutable paths that bypass `mutationPolicy.allowed`;
- protected content not covered by an integrity boundary;
- agent-controlled success without deterministic validation;
- mutable phases without a resolved validation gate;
- repair policies that accidentally apply to hard/safety gates;
- criterion phases without stable criteria or progress invariants;
- actor edits to engine-owned progress;
- completion markers written before final validation/checkpoint/post-checks;
- human gates without durable evidence;
- bookkeeping before required implementation/audit/human prerequisites;
- `requiresChange: false` phases incorrectly treated as requiring a diff;
- legacy lifecycle/recovery actions that bypass the runtime-owned safe contract;
- expressions or runtime constructs outside the documented supported surface.

Report only issues supported by the YAML and bundled specification. Label inferred risks as inferences.

## Comparison mode

Compare these semantic dimensions:

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

Call out semantic differences even when field layout differs.

## Efficiency rules

- In authoring mode, read the authoring reference before the long field guide.
- In description mode, read control fields before prompts.
- Do not restate defaults repeatedly for every phase.
- Distill prompts to one sentence per phase.
- Prefer execution order over YAML declaration order.
- Expand only the section relevant to the user's question.
- Preserve exact declared terminology when precision matters.
- Do not infer undocumented engine behavior.
- Do not inspect AgentFlow source code as a substitute for the bundled public contract.
