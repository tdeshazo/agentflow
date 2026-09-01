---
name: agentflow-spec
description: Create, modify, explain, review, validate, or compare `agentflow.dev/v1alpha4` `AgentWorkflow` YAML specifications. Use for AgentFlow workflow authoring, authority review, deterministic validation, recovery, typed handoffs, and typed work items. Produce v1alpha4 syntax only.
---

# AgentFlow v1alpha4 specifications

Produce executable `agentflow.dev/v1alpha4` specifications from the public
contract. Keep actor intent, deterministic acceptance, workspace authority,
human evidence, work-item progress, and durable completion distinct.

## Contract boundary

- Author only `apiVersion: agentflow.dev/v1alpha4` with
  `kind: AgentWorkflow`.
- Read the checked-in [v1alpha4 authoring contract](../../docs/reference/agentflow-v1alpha4.md)
  and consult the [executable schema](../../schema/v1alpha4.schema.json) when
  exact field structure matters.
- Do not author, quote, preserve, or recommend syntax from earlier API
  versions. When an input uses older syntax, translate its intended behavior
  into v1alpha4 and identify behavior that requires redesign.
- Treat the public contract and schema as the specification surface. Do not
  inspect `internal/`, `provider/`, tests, or other implementation source to
  invent fields or runtime behavior.
- If a documented construct is rejected, report contract/runtime drift rather
  than reverse-engineering a workaround.

## Modes

- **Author or modify:** Produce valid executable YAML. Deterministic workflow
  logic—not actor prose—decides acceptance and advancement.
- **Describe:** Report only authority and behavior declared by the YAML.
- **Review:** Check reference integrity, mutation authority, deterministic
  acceptance, typed handoffs, work-item progress, recovery, human evidence,
  and completion ordering.
- **Compare:** Compare v1alpha4 semantics rather than formatting or declaration
  order.

## Author or modify

1. Define the bounded outcome, workspace, allowed and protected paths,
   deterministic acceptance commands, human evidence, finite work items, and
   durable completion condition. Parameterize unavailable repository facts.
2. Declare sections in dependency order: metadata; parameters; workspace;
   execution; agents; tools; preconditions; validation; artifacts; evidence;
   criteria; phases; human gates; completion; reset. Omit unused optional
   sections and use stable IDs.
3. Separate authority: actors attempt work; workspace policy limits mutations;
   validation accepts phases; the runtime advances exact work items; humans
   provide only necessary evidence; completion owns terminal state. Every
   allowlisted path needs an actor outcome or engine-owned transition.
4. Bound each prompt to its outcome, write scope, exclusions, local checks, and
   whether a diff is required. Put every gate-enforced filename, heading,
   symbol, label, or literal in the prompt or an actor-readable contract.
5. Allocate model capability and reasoning by task risk and ambiguity. Keep an
   independent final reviewer separate from implementation and repair actors;
   ephemeral execution does not erase authorship.
6. Give every mutable AI phase a deterministic validation with at least one
   step. Use hard gates where repair is unsafe or a bounded `repair: once` and
   rerun the gate after repair. A repair after review requires another
   independent review.
7. Declare typed artifacts and evidence explicitly. Consumers receive only
   declared direct inputs; do not rely on transcripts, final messages, or broad
   run history as authority.
8. Declare finite criteria with stable IDs and exactly one
   `advanceWorkItem: true` phase per item. Use `forEach` only for a statically
   declared collection and set `maxItems` to its exact size. A
   `markdownChecklist` is a runtime-owned mirror; actors never edit it.
9. Add human gates only where automation cannot establish the evidence.
   Specify timing, procedure, acknowledgement, and intentional skip behavior.
10. Treat completion as a separate transition. Author only `assertions`,
    `validation`, and `evidence` under `completion`. The runtime owns the
    subsequent checkpoint, post-checks, durable completion marker, and summary;
    inspect those generated transitions in the expanded plan rather than
    declaring lifecycle fields in the workflow.
11. Design fresh initialization, active-phase recovery, accepted-phase resume,
    completion retry, already-complete invocation, and reset together.
    Initialization-only mutable facts must not become unconditional resume
    preconditions.
12. Preserve unrelated semantics when modifying a workflow and state any
    assumption that changes scope, validation, human verification, or
    completion.

Use `spec.workspace.allowWrites` for workflow-wide mutation authority and
`phases[].writes` to narrow individual phases. Protect content separately with
named `spec.workspace.integrity` rules using a supported hash mode and nonempty
paths.

## Store and select workflows

Store repository-owned workflows under `<repository>/.agentflow/workflows/`
and user-wide workflows under `~/.agentflow/workflows/`. Repository-local names
take precedence. Select a stored workflow by filename without its extension:

```sh
agentflow validate release-check
agentflow plan --expanded release-check
```

Use `-f path/to/workflow.yaml` for an intentionally external document. Do not
combine `-f` with a positional selector.

## Validate

Validate without running actors:

```sh
agentflow validate -f workflow.yaml
agentflow plan --expanded -f workflow.yaml
```

Inside the AgentFlow source repository, use:

```sh
go run . validate -f workflow.yaml
go run . plan --expanded -f workflow.yaml
```

Fix every `invalid` diagnostic. Replace unsupported constructs unless the user
explicitly requested a non-executable design. Inspect the expanded plan for
resolved models and reasoning, phase and repair independence, mutation scope,
validation and retry budget, typed inputs, exact work-item transitions,
runtime-owned checkpoint behavior, human gates, and completion transitions.
Confirm every unconditional precondition remains true across safe resume states
and every named tool is executable in its invocation context.

Do not run a workflow merely to learn whether its YAML is valid.

## Output expectations

- Return or write one complete v1alpha4 `AgentWorkflow` unless fragments were
  requested.
- Prefer concise defaults and runtime-owned safe resume.
- Keep comments for non-obvious safety or authority decisions.
- Keep actor prompts shorter than orchestration logic.
- Parameterize unknown repository facts instead of fabricating paths or
  commands.

## Describe

Read control fields before prompts. Explain in this order: owned outcome and
completion; mutation and integrity boundaries; actors and deterministic tools;
phase order and exact work-item advancement; failure and recovery; human
verification; terminal completion.

For each phase, report only its ID, kind, actor/reasoning, work item, change
requirement, and one-sentence intent. Describe the resolved acceptance pipeline
in execution order, typically:

`actor → scope/integrity → deterministic gate → bounded repair → progress/net-change → checkpoint → phase evidence`

Distinguish provider failure, gate failure, bounded repair, exhausted repair,
integrity failure, interruption recovery, and completion retry.

## Review checklist

Check for:

- any API version other than `agentflow.dev/v1alpha4`;
- unknown fields, unsupported tools, malformed expressions, or dangling IDs;
- mutable paths outside `workspace.allowWrites` or `phases[].writes`;
- allowlisted paths with no actor or runtime-owned outcome;
- protected paths missing named integrity rules, including private local
  control files that affect execution;
- mutable initialization facts imposed on every resume;
- actor-controlled acceptance, progress, checklist state, or completion;
- mutable AI phases without deterministic validation;
- gate-enforced literals hidden from the responsible actor;
- one-shot repair that cannot discover the gate's complete failure set;
- repairable final review without a subsequent independent review;
- a reviewer reused as an implementation or repair author;
- artifacts or evidence consumed without declared typed inputs;
- work items without exactly one advancing phase;
- unbounded or dynamically discovered `forEach` collections;
- actor edits to runtime-owned checklist presentation;
- phase scopes that overlap unnecessarily under parallel execution;
- completion entries with fields other than `assertions`, `validation`, or
  `evidence`, including attempts to author runtime-owned checkpoint, marker, or
  summary transitions;
- terminal validation that proves only formatting, cleanliness, or an empty
  diff rather than semantic acceptance;
- `requiresChange: false` phases treated as requiring a diff;
- resume paths that rerun accepted actors or invalidate mutable preconditions.

Report only defects supported by the YAML and public v1alpha4 contract. Label
inferences and preserve exact declared terminology.
