# AgentFlow v1alpha2 authoring contract

This document defines the concise authoring contract for
`agentflow.dev/v1alpha2`. It is a concise AgentFlow evolution: the reference
implementation strictly decodes the form, validates it as executable, and
normalizes it to AgentFlow's shared authority model before planning or
execution. The runtime uses the declared phase dependencies with a
deterministic serial scheduler.

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
| `validation.<name>.run` | Deterministic shell validation gate |
| `validation.<name>.repair.once` | One bounded repair attempt followed by the same validation |
| `phases[].validation` | Phase acceptance validation |
| `phases[].dependsOn` | Accepted-phase dependency evidence |
| `completion.validation` | Deterministic final validation gate |

Normalization must preserve the authority boundary: actors can attempt work,
workspace policy limits mutations, and deterministic validation authorizes
advancement. Normalization must not create a second execution model or allow
actor output to become acceptance evidence.

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

    reviewer:
      runner: codex
      model: gpt-5.6-luna

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
      actor: reviewer
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

`agents` is a map of named actor capabilities. A phase selects an actor with
`actor`. The v1alpha2 core requires the selected actor to declare a `runner`
and `model`; the runner and model identify how the actor is invoked, not
whether its work is accepted.

Every phase actor reference must resolve to an entry in `spec.agents`.
Missing actors are structural errors and fail closed before actor execution.

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
- `actor`, naming a declared agent;
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

The v1alpha2 core is executable, but its initial scheduler is deliberately
serial. It does not yet provide:

- parallel execution of independent phases;
- implicit mutation authority outside `workspace.allowWrites`; or
- acceptance based on actor output, commits, or an unvalidated workspace.

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
