---
name: agentflow-spec
description: Create, modify, explain, review, validate, or compare AgentFlow `agentflow.dev/v1alpha1` `AgentWorkflow` YAML specifications. Use when turning a task or process into an AgentFlow workflow; changing an existing workflow; explaining, auditing, or comparing workflow behavior; or diagnosing specification validation, safety, resumability, or design problems.
---

# AgentFlow specifications

Produce executable specifications from the public AgentFlow contract. Keep agent intent, deterministic acceptance, workspace authority, human evidence, and durable completion distinct.

## Use the contract

- Treat the workflow YAML and bundled references as the specification surface.
- Do not inspect `internal/`, `provider/`, tests, or other implementation source to discover fields or runtime behavior, unless the user explicitly asks to debug or change AgentFlow itself.
- Never invent fields, tool types, expressions, phase kinds, or acceptance semantics.
- If a documented construct is rejected, report the specification/documentation drift rather than reverse-engineering a workaround.

Read resources progressively:

- Read [the authoring guide](references/authoring-agentflow-v1alpha1.md) before authoring or modifying a workflow. It contains supported values, patterns, templates, and the validation loop.
- Read [the field guide](references/agentflow-v1alpha1.md) only for a field’s semantics, an expanded-plan interpretation, or a review question that needs detail.

## Modes

Choose the requested mode:

- **Author or modify:** Create valid, executable YAML. Let deterministic workflow logic—not an agent’s success message—decide advancement.
- **Describe:** Treat YAML as the source of truth. Do not infer undeclared capability, mutation authority, retry, or completion behavior.
- **Review:** Check reference integrity, authority boundaries, deterministic acceptance, recovery, human evidence, and completion ordering.
- **Compare:** Compare semantics, not formatting or declaration order.

## Author or modify a workflow

1. Define the bounded outcome, workspace/repository, allowed and protected paths, acceptance command, human evidence (if any), and durable completion condition. Parameterize unavailable repository facts instead of inventing them.
2. Select the simplest fit: a single implementation phase; implementation plus audit; a criterion workflow; a human-gated workflow; bookkeeping/completion; or, only when required, a dynamic criterion loop. Prefer `spec.defaults` with `spec.lifecycle.policy: safe-resume` for new workflows.
3. Declare sections in dependency order: metadata; parameters/paths; workspace/state; defaults/agents; tools; preconditions; validation; progress; phases; human gates; completion; flow. Use stable IDs.
4. Separate authority: agents attempt work; workspace policy controls mutations; deterministic validations accept phases; humans provide only necessary human evidence; completion owns terminal state.
5. Bound each prompt to its outcome, scope, local checks, exclusions, and whether a diff is required. Keep acceptance logic outside the prompt.
6. Give every mutable AI phase a resolved deterministic validation gate. Give every validation at least one deterministic step. Use no repair for hard gates or a bounded `repair: once`; rerun the gate after repair.
7. Add checklist progress only for real criteria. Use stable criterion IDs and engine-owned `advanceProgress: true`; do not ask actors to edit engine-owned progress.
8. Add human gates only where automation cannot establish the evidence. Specify timing, procedure, acknowledgement, durable evidence, and intentional skip behavior.
9. Treat completion as a separate transition. When durable completion is required, prefer `assertions → finalValidation → checkpoint → afterCheckpointAssertions → writeMarker → summary`, writing the marker last.
10. Design the same document for fresh initialization, active-phase recovery,
    accepted-phase resume, completion retry, already-complete invocation, and
    reset. Unconditional preconditions must remain true in every state that can
    safely resume. Use `scope: initialization` for mutable facts required only
    when establishing fresh durable state, and documented phase/flow
    eligibility for actor dispatch.
11. Preserve unrelated semantics when modifying a workflow and state assumptions that materially affect scope, validation, human verification, or completion.

For protected content, declare `spec.workspace.mutationPolicy.integrity` as a
list of named rules. Each rule has `id`, `mode`, and non-empty `paths`; the
allowlist and integrity rules are separate boundaries. Use
`spec.workspace.mutationPolicy.integrity` as the assertion policy when the
whole protected rule set must be checked.

## Store and select workflows

To make a workflow selectable by name, save its `.yaml` or `.yml` file in one
of these directories:

- `<repository>/.agentflow/workflows/` for a workflow owned by one repository.
- `~/.agentflow/workflows/` for a workflow available across the current user's
  repositories.

Repository-local workflows take precedence when the two directories contain
the same filename. From the target repository, use the filename without its
extension as the positional selector, for example:

```sh
agentflow validate release-check
agentflow plan --expanded release-check
```

Use `-f path/to/workflow.yaml` when the workflow is intentionally outside
these directories. Do not combine `-f` with a positional workflow name.

## Validate an executable specification

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

Also inspect each unconditional precondition against the resume states above.
Confirm that every named tool use is executable in its specific context;
successful reference resolution alone does not establish completion-assertion
support.

Do not run a workflow merely to learn whether its YAML is valid.

## Authoring output expectations

When asked to create a workflow:

- Return or write one complete AgentWorkflow YAML document unless the user asks for fragments. For named discovery, `workflow-name.yaml` or `workflow-name.yml` is sufficient.
- Use the current `agentflow.dev/v1alpha1` / `AgentWorkflow` identifiers.
- Prefer concise defaults and runtime-owned safe resume.
- Include comments only where they explain a non-obvious safety or authority decision.
- Keep prompts shorter than orchestration logic.
- If repository facts are unavailable, parameterize them rather than fabricating file paths or commands.
- State any assumptions that materially affect mutation scope, validation, human verification, or completion.

When asked to modify a workflow, preserve existing semantics outside the requested change and rerun the validation loop.

## Describe a workflow

Read control fields before prompts: `metadata`, parameters, workspace, agents, validation, defaults/lifecycle (or legacy phase defaults), phases, human gates, recovery, flow, and completion. Read state, preconditions, progress, and tools only when relevant.

Explain in this order: owned outcome/completion; mutation and integrity boundaries; actors and deterministic tools; execution order; advancement; failure/recovery; human verification; terminal completion. Prioritize control semantics over repeating prompts.

Always distinguish agent, workspace, and validation authority. State that committing does not grant acceptance authority. For each phase, report only its ID, kind, label, actor/reasoning, criterion (if any), change requirement, and one-sentence intent. Describe the resolved acceptance pipeline in execution order—typically `agent run → scope/integrity checks → deterministic gate → bounded repair → progress/net-change assertions → checkpoint → completed-phase evidence` for safe-resume—and distinguish hard failure, bounded repair, exhausted repair, integrity failure, and interruption recovery.

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

## Review or compare workflows

Check for:

- unknown or undocumented fields/types;
- unresolved or dangling named references;
- mutable paths that bypass `mutationPolicy.allowed`;
- protected content not covered by an integrity boundary;
- ignored local control files (including the selected workflow, repository
  instructions, and authoring skills) that affect execution without an
  explicit integrity rule, or rules that zero-match; symlink rules protect the
  link object, not external target contents;
- mutable initial facts left as unconditional preconditions instead of
  `scope: initialization`, causing accepted-phase or completion retries to
  fail;
- named completion assertions whose referenced tool type is not documented as
  executable in assertion context;
- agent-controlled success without deterministic validation;
- mutable phases without a resolved validation gate;
- validation commands that are vacuous at their execution point, such as a
  worktree-only diff check after the lifecycle has checkpointed a clean tree;
- repair policies that accidentally apply to hard/safety gates;
- criterion phases without stable criteria or progress invariants;
- workflows claiming the "next" roadmap criterion without deterministic
  preconditions for stable roadmap order/dependencies and an
  initialization-scoped exact-pending eligibility check;
- actor edits to engine-owned progress;
- single-criterion completion without deterministic evidence that its exact
  target is checked (when `progress-empty` would incorrectly require later
  criteria too);
- completion markers written before final validation/checkpoint/post-checks;
- completion failures after a successful phase checkpoint that cannot be
  retried without rerunning actors or violating a now-stale precondition;
- terminal validation that proves only scope, formatting, cleanliness, or an
  empty diff instead of re-running the canonical semantic acceptance gate;
- human gates without durable evidence;
- bookkeeping before required implementation/audit/human prerequisites;
- `requiresChange: false` phases incorrectly treated as requiring a diff;
- legacy lifecycle/recovery actions that bypass the runtime-owned safe contract;
- expressions or runtime constructs outside the documented supported surface.

Report only issues supported by YAML and the bundled contract; label inferences. For comparisons, cover state/resume, mutation and integrity rules, agent/model allocation, phase granularity, deterministic gates, repair budget, progress invariants, checkpoint strategy, human verification, and completion contract. Call out semantic differences even when field layout differs.

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
