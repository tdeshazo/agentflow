# AgentFlow v1alpha2 authoring contract

This document defines the concise authoring contract for
`agentflow.dev/v1alpha2`. It is a concise AgentFlow evolution: the reference
implementation strictly decodes the form, validates it against the current
runtime surface, and normalizes it to AgentFlow's shared authority model
before planning or execution. A structurally valid workflow that selects an
unsupported built-in approval policy is reported as unsupported; runner names
remain provider-neutral for injected Go providers. The runtime uses the
declared phase dependencies with a deterministic serial scheduler.

v1alpha2 is an evolution of AgentFlow's existing vocabulary and authority
boundaries. It keeps `workspace`, `agents`, `validation`, `phases`, and
`completion` as the primary nouns. It does not replace them with a generic
workflow vocabulary.

## Compatibility

`agentflow.dev/v1alpha1` remains supported with its existing behavior. A
v1alpha1 document is not silently interpreted as v1alpha2, and v1alpha2 does
not change the meaning of any v1alpha1 field. Implementations select the
contract from `apiVersion` and fail closed when a document uses fields or
references outside the selected contract. The checked-in conformance example
is [`internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml`](../../internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml).

The v1alpha2 form is intentionally concise. Its fields normalize to
AgentFlow's existing executable authority concepts wherever possible:

| v1alpha2 authoring field | Existing authority concept |
| --- | --- |
| `workspace.allowWrites` | Workspace mutation allowlist/policy |
| `agents.<name>` | Named actor capability |
| Mapping-valued `phases[].actor` | Phase-local actor capability lowered to an internal named agent |
| `validation.<name>.run` | Deterministic shell validation gate |
| `validation.<name>.repair.once` | One bounded repair attempt followed by the same validation |
| `phases[].validation` | Phase acceptance validation |
| `phases[].dependsOn` | Accepted-phase dependency evidence |
| `completion.validation` | Deterministic final validation gate |

Normalization must preserve the authority boundary: actors can attempt work,
workspace policy limits mutations, and deterministic validation authorizes
advancement. Normalization must not create a second execution model or allow
actor output to become acceptance evidence.

v1alpha2 has capability parity for the shared agent execution fields, not full
v1alpha1 schema parity. In particular, the v1alpha1 `spec.defaults.agent`
inheritance layer and other v1alpha1-only defaults/schema fields remain outside
the v1alpha2 contract. v1alpha2 agents declare their effective capability
values directly.

## Canonical authoring form

The intended v1alpha2 form is:

```yaml
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow

metadata:
  name: feature

spec:
  workspace:
    allowWrites: [src/**, tests/**]

  agents:
    coder:
      runner: codex
      model: gpt-5.6-terra
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: true
      output_last_message: true

  validation:
    tests:
      run: make test
      repair:
        once: coder

  phases:
    - id: implement
      actor: coder
      prompt: Implement the feature.
      validation: tests

    - id: review
      actor:
        runner: codex
        model: gpt-5.6-luna
        sandbox: workspace-write
        approval: never
        ephemeral: true
        may_commit: false
        output_last_message: true
      dependsOn: [implement]
      prompt: Review the feature.
      validation: tests

  completion:
    validation: tests
```

`metadata.name` identifies the workflow. Each phase `id`, agent name, and
validation name is a stable reference within the document.

## Authoring fields

### `spec.workspace.allowWrites`

`allowWrites` is the concise mutation authority for the workflow. Its values
are workspace-relative path patterns. The normalized form is the existing
workspace mutation policy allowlist; it is not merely descriptive metadata.

An actor may change only what the normalized workspace policy permits. A
successful actor return, a commit, or a claim in actor output cannot widen the
allowlist. Scope and protected-resource checks remain part of the existing
acceptance boundary.

### `spec.agents`

`agents` is a map of named actor capabilities. A phase may select one by name,
or may declare a phase-local actor mapping. Named and inline agents use exactly
the same v1alpha2 schema. Both require `runner` and `model`; both also accept
the actor capability fields below. These fields identify how the actor is
invoked, not whether its work is accepted.

The v1alpha2 capability fields preserve the meanings of the shared v1alpha1
and runtime `Agent` type. v1alpha2 does not introduce provider-independent
values, defaults, or other semantics for them:

| Field | Type | Meaning |
| --- | --- | --- |
| `sandbox` | string | Selects the provider sandbox capability. |
| `approval` | string | Selects the provider approval policy, subject to provider support. |
| `ephemeral` | boolean | Controls provider session/context persistence according to existing provider semantics. |
| `may_commit` | boolean | Controls whether the actor capability may create commits according to existing AgentFlow checkpoint/workspace policy. |
| `output_last_message` | boolean | Requests final-message retention at the provider boundary; the current Codex adapter captures a final message for every invocation. The message is never acceptance evidence. |

An explicit boolean `false` is a valid authored value. In particular,
`may_commit: false`, `output_last_message: false`, and `ephemeral: false` are
not missing fields and must not be replaced by truthy defaults. v1alpha2 does
not apply inherited agent defaults: omitted boolean fields normalize to `false`,
while an explicit `false` remains `false`.

These are actor execution capabilities, not acceptance authority. None of
these fields can authorize phase acceptance, satisfy `dependsOn`, waive
validation, widen `workspace.allowWrites`, bypass integrity or lineage checks,
extend a repair budget, or authorize workflow completion. Those decisions
remain owned by the existing workspace, deterministic validation, dependency,
repair, integrity, lineage, and completion contracts.

Every scalar phase actor reference must resolve to an authored entry in
`spec.agents`. Missing actors are structural errors and fail closed before
actor execution.

An inline actor is lowered into the ordinary shared `Workflow` agent map, and
the executable phase receives a scalar reference to that generated agent.
There is no second runtime actor representation. The generated name is
deterministic: `__inline_actor__` followed by the phase ID (or its authored
index when the ID is absent). The prefix is runtime-owned. Authored agent
names, scalar phase actor references, and `repair.once` references must not use
it, including through YAML aliases. Generated-name collisions and unknown
inline-agent fields fail closed.

### `spec.validation`

`validation` remains singular and is a map of named deterministic acceptance
gates. Each v1alpha2 validation has a `run` command interpreted as a shell
validation. The command's exit status and deterministic validation evidence,
not actor output, determine whether the gate passes.

The `run` command is deterministic validation by contract: it is a repository-
owned check whose result can be repeated against the relevant workspace. A
validation without a usable `run` command is invalid.

#### Bounded repair

`repair.once: <actor>` has exactly these semantics when the initial validation
fails as a validation failure:

1. Invoke the named actor for one repair attempt.
2. Run the same deterministic validation again.
3. Accept the validation only if that revalidation succeeds.

The named actor must resolve in `spec.agents`. There is no second repair
attempt, implicit retry, or actor-controlled bypass. Repair actor success is
never acceptance; it only permits the one deterministic revalidation. A
failure of that revalidation fails the phase or final completion gate as
appropriate. Safety, scope, integrity, and structural failures remain
terminal and do not become repair invitations.

### `spec.phases`

`phases` is an ordered list of named units of actor work. Each phase has:

- `id`, which must be unique within the workflow;
- `actor`, either naming a declared agent or declaring one inline with the
  v1alpha2 agent schema;
- `prompt`, describing the bounded work intent; and
- `validation`, naming a declared deterministic validation.

`dependsOn` is optional. Its values are phase IDs. It describes readiness, not
mere presentation order:

> A phase may become ready only after every referenced phase has been
> deterministically accepted.

For a dependency to be accepted, the referenced phase must have completed its
actor attempt, scope and policy checks, named deterministic validation,
required checkpoint/evidence handling, and any other applicable acceptance
conditions. The following never satisfies a dependency on its own:

- actor return or actor output;
- a commit made by the actor;
- a phase that was invoked but not validated; or
- an unvalidated workspace state.

The dependency graph must fail closed for unknown dependency IDs,
self-dependencies, duplicate phase IDs, and dependency cycles. A phase with a
missing actor or missing validation also fails closed before execution.
An unresolved `completion.validation` reference is likewise a structural error
and fails closed before the completion transition.

### `spec.completion.validation`

`completion.validation` names the deterministic final validation gate. It is
run as a distinct completion transition after the required phases have been
accepted. Successful validation evidence from an earlier phase does not, by
itself, satisfy the final completion validation: the final gate must establish
acceptance for the final workspace state and completion boundary.

If the final validation has `repair.once`, its repair actor is subject to the
same exactly-one-attempt and deterministic-revalidation rules. The final
completion state is written only after the final validation succeeds and all
other existing completion conditions pass. Its validation evidence, failed
validation record, and repair budget are scoped to the completion transition,
so they cannot be borrowed from a phase that uses the same validation name.

## Dependency-derived execution

`spec.flow` is optional in v1alpha2. When it is omitted, execution is derived
from the phase dependency graph:

1. Validate all phase IDs, references, and cycles before actor execution.
2. Mark no phase accepted until its own deterministic acceptance contract
   succeeds.
3. A phase is ready only when every `dependsOn` phase is accepted.
4. Run ready phases with the deterministic serial scheduler.
5. When more than one phase is ready, choose declaration order as the stable
   tie-breaker.
6. Continue until every phase is accepted or a phase/validation fails.

The initial v1alpha2 contract does not include parallel phase execution.
Independent phases may be represented in the graph, but they are still
executed serially. Future concurrency work must preserve the same dependency
and acceptance semantics and requires a separate contract/runtime change.

The v1alpha2 core does not include `spec.flow`. The dependency graph is the
source of its serial schedule; a later scheduling extension must preserve the
same readiness and acceptance boundary.

## Authority invariants

The following are normative for v1alpha2:

- Actor/model output never authorizes phase advancement.
- Actor/model output never authorizes workflow completion.
- Workspace mutation authority comes from the normalized workspace policy.
- Phase advancement requires the phase's named deterministic validation.
- Repair actor success never constitutes acceptance.
- Dependency readiness requires deterministic acceptance of every dependency.
- Final completion requires the distinct named completion validation.
- Structural ambiguity or an unsafe reference fails closed.

These rules are authoring semantics, not prompt conventions. Validation
diagnostics and the expanded execution plan make them visible before any actor
executes.

## Current runtime boundary

The v1alpha2 core is executable when it uses the current runtime surface, but
its initial scheduler is deliberately serial. It does not yet provide:

- parallel execution of independent phases;
- implicit mutation authority outside `workspace.allowWrites`; or
- acceptance based on actor output, commits, or an unvalidated workspace.

The built-in Codex provider reports approval policies other than `never` as
valid but unsupported. Runner names remain provider-neutral in v1alpha2 so
injected Go providers can be used through the Go API; the CLI's built-in
provider registry still determines which runner can execute a run.

`v1alpha1` compatibility remains a separate contract and regression coverage
continues to ensure that v1alpha2 fields such as `dependsOn` are not accepted
under v1alpha1.

## CLI inspection

Validate and inspect the normalized contract without opening a repository or
invoking an actor:

```sh
agentflow validate -f examples/feature.agent-workflow.yaml
agentflow plan --expanded -f examples/feature.agent-workflow.yaml
```

The expanded plan exposes resolved named actors, the workspace authority,
bounded repair behavior, dependency edges and their acceptance condition, the
phase acceptance boundary, deterministic final validation, and durable
completion behavior.
