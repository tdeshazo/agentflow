# Concise authoring syntax

AgentFlow keeps actor authority, workspace mutation authority, deterministic
validation authority, and completion authority structurally separate. Concise
authoring syntax may remove boilerplate, but it must compile to the same
executable contract and must not create an acceptance bypass.

## v1alpha2 concise evolution

`agentflow.dev/v1alpha2` is the concise AgentFlow evolution for a small,
dependency-aware implementation and review workflow. It keeps the existing
AgentFlow nouns—`workspace`, `agents`, `validation`, `phases`, and
`completion`—and makes the executable authority explicit in a compact form:

```yaml
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata:
  name: feature
spec:
  workspace:
    allowWrites: [src/**, tests/**]
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
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
      dependsOn: [implement]
      prompt: Review the feature.
      validation: tests
  completion:
    validation: tests
```

This form is executable today. `allowWrites` normalizes to the existing
workspace mutation policy, named and inline actors resolve to the existing
executor capabilities, and each `run` command becomes a deterministic shell
validation.
`repair.once` permits exactly one named repair attempt and then reruns the same
validation. `dependsOn` requires durable deterministic acceptance of every
referenced phase, and the completion validation is a separate final transition.
The expanded plan makes these normalized boundaries inspectable before a run.

See the [v1alpha2 reference](../reference/agentflow-v1alpha2.md) and the
[checked-in conformance example](../../internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml).

The conveniences shared by both language versions are recorded explicitly:

| Concise authoring convenience | v1alpha1 | v1alpha2 |
| --- | --- | --- |
| `workspace.allowWrites` | Supported through concise AST lowering | Supported by the v1alpha2 schema and direct lowering |
| `validation.<name>.run` | Supported through concise AST lowering | Supported by the v1alpha2 schema and direct lowering |
| Mapping-valued `phases[].actor` | Supported with the v1alpha1 `Agent` schema | Supported with the v1alpha2 `agents.<name>` schema |

Each version lowers these fields through its own authoring implementation into
the shared executable `Workflow` model. v1alpha2 is not passed through the
v1alpha1 concise AST rewrite.

## Workspace write allowlist

For workflows that only need a path allowlist, `workspace.allowWrites` lowers
to `workspace.mutationPolicy.allowed` in the shared executable model:

```yaml
spec:
  workspace:
    allowWrites:
      - src/**
      - tests/**
```

The resulting executable authority is equivalent to:

```yaml
spec:
  workspace:
    mutationPolicy:
      allowed:
        - src/**
        - tests/**
```

Do not declare both forms in the same workflow. AgentFlow rejects the document
instead of merging them because an implicit merge would make the effective
mutation authority harder to review.

Use the full `mutationPolicy` form when the workflow also needs protected
integrity rules, ignored control files, or explicit lineage policy.

## Inline one-off actors

Reusable actors remain named under `spec.agents` and referenced by name:

```yaml
spec:
  agents:
    reviewer:
      runner: codex
      model: gpt-5.6-terra

  phases:
    - id: review
      kind: audit
      actor: reviewer
      validation: tests
      prompt: Review the implementation.
```

A phase that needs a one-off actor may instead place the ordinary `Agent`
fields for its selected language version directly under `actor`. In v1alpha2,
the mapping uses exactly the same full schema as a named v1alpha2 agent,
including `runner`, `model`, `sandbox`, `approval`, `ephemeral`, `may_commit`,
and `output_last_message`:

```yaml
spec:
  phases:
    - id: review
      actor:
        runner: codex
        model: gpt-5.6-terra
        sandbox: workspace-write
        approval: never
        ephemeral: true
        may_commit: false
        output_last_message: true
      validation: tests
      prompt: Review the implementation.
```

AgentFlow compiles the mapping-valued actor into an internal named agent and
rewrites the phase to reference it. The generated agent then follows the same
normalization, dependency scheduling, validation, and runtime path as any
explicitly named actor. Unknown inline fields are rejected by the same strict
schema used for named agents.

In v1alpha1, the mapping instead uses the v1alpha1 `Agent` schema, including
fields such as `sandbox`, `approval`, `ephemeral`, and `may_commit`.
`defaults.agent` inheritance and explicit boolean overrides continue to apply
there unchanged.

The v1alpha2 capability fields control actor execution only. They preserve the
shared v1alpha1/runtime `Agent` meanings and do not grant acceptance authority:
they cannot accept a phase or satisfy `dependsOn`, waive validation, widen
`workspace.allowWrites`, bypass integrity or lineage, extend repair budgets, or
authorize workflow completion. Explicit `false` values for `ephemeral`,
`may_commit`, and `output_last_message` remain valid values rather than missing
fields or truthy defaults. See the [v1alpha2 reference](../reference/agentflow-v1alpha2.md#specagents)
for the field-level contract.

The generated name uses the reserved `__inline_actor__` prefix and the phase ID
when available. That namespace is runtime-owned: authored workflows must not
declare named agents with that prefix or reference such names from phases,
phase defaults, or repair policies, including through YAML aliases. Generated
name collisions are rejected. This keeps a generated one-off capability local
to its phase instead of turning a predictable internal name into a workflow
API.

Use a named actor when multiple phases share an execution capability, when a
repair policy or other workflow object needs to reference the actor, or when the
capability should have a stable human-facing identity. Use the inline form for a
capability that is genuinely local to one phase.

## v1alpha1 concise syntax

The following shorthand sections describe the existing v1alpha1 authoring
layer. They remain compatible with v1alpha1 and are separate from the
v1alpha2 contract above.

Concise preprocessing is opt-in by syntax: v1alpha1 workflows that use none of
these shorthands are decoded from their original YAML bytes through the
ordinary strict `KnownFields` decoder. A shorthand document is rewritten to
the canonical v1alpha1 shape before that same strict decode. Folded scalar
semantic values are preserved during the rewrite.

### Inline shell validation

A validation containing one shell command may use `run` instead of declaring a
separate shell tool and a one-element `steps` list:

```yaml
spec:
  validation:
    tests:
      run: go test ./...
      repair: once
```

This is equivalent to the canonical executable form:

```yaml
spec:
  tools:
    tests-command:
      type: shell
      command: go test ./...

  validation:
    tests:
      repair: once
      steps:
        - uses: tests-command
```

The shorthand is compiled to an internal shell tool and an ordinary validation
step before the runtime executes the workflow. Deterministic validation,
validation evidence, bounded repair, lifecycle acceptance, and completion all
continue to use the existing executable semantics.

`run` and `steps` are mutually exclusive. Use `steps` when a validation needs
multiple deterministic operations, conditional tool uses, typed `with`
arguments, or reusable named tools.

## YAML merge keys

Concise authoring expansion intentionally fails closed around YAML merge keys.
A mapping that the preprocessor must inspect or modify may not contain `<<`.
For example, this is rejected instead of silently allowing the generated
`steps` value to shadow inherited validation steps:

```yaml
validation:
  shared: &shared
    steps:
      - uses: existing-check
  gate:
    <<: *shared
    run: go test ./...
```

Use the canonical form explicitly when YAML merge behavior is required. The
restriction is deliberately local to mappings touched by concise expansion; it
prevents shorthand conflict checks from depending on implicit merge precedence.

## Example

A small repository workflow can therefore keep the authority boundaries while
remaining compact and can mix reusable named actors with one-off inline actors:

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: feature

spec:
  workspace:
    allowWrites: [src/**, tests/**]

  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true
    lifecycle:
      policy: safe-resume
    repair:
      actor: coder
      reasoning: high
      prompt: Repair only the deterministic validation failure.

  agents:
    coder:
      model: gpt-5.6-terra

  validation:
    tests:
      run: go test ./...
      repair: once

  phases:
    - id: implement
      kind: implementation
      actor: coder
      validation: tests
      prompt: Implement the requested feature and focused tests.

    - id: review
      kind: audit
      actor:
        model: gpt-5.6-terra
        may_commit: false
      validation: tests
      prompt: Independently review the implementation.

  completion:
    done:
      finalValidation: tests

  flow:
    - phase: implement
    - phase: review
    - complete: done
```

The authored form is intentionally shorter, but the semantics remain:

1. actors may mutate only the allowed workspace paths and only with their
   resolved capabilities;
2. an actor's successful return does not accept the phase;
3. the deterministic test command authorizes advancement;
4. a failed validation receives only its declared repair budget; and
5. completion still requires the configured deterministic final validation.

## What is not shorthand

The v1alpha1 concise layer does not add `dependsOn`. Dependency edges belong
to the v1alpha2 contract, where the ready-node scheduler and durable acceptance
semantics enforce them. A v1alpha1 document that declares `dependsOn` remains
invalid rather than being silently interpreted as v1alpha2.

Similarly, concise validation syntax does not add model-decided completion,
`skip`-on-failure acceptance, or unbounded repair. Those behaviors would weaken
the deterministic acceptance boundary rather than merely shorten YAML.
