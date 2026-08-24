# Concise authoring syntax

AgentFlow keeps actor authority, workspace mutation authority, deterministic
validation authority, and completion authority structurally separate. Concise
authoring syntax may remove boilerplate, but it must compile to the same
executable contract and must not create an acceptance bypass.

The `v1alpha1` authoring surface currently includes three additional shorthands
for common repository workflows.

## Workspace write allowlist

For workflows that only need a path allowlist, `workspace.allowWrites` is
shorthand for `workspace.mutationPolicy.allowed`:

```yaml
spec:
  workspace:
    allowWrites:
      - src/**
      - tests/**
```

This is equivalent to:

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
fields directly under `actor`:

```yaml
spec:
  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true

  phases:
    - id: review
      kind: audit
      actor:
        model: gpt-5.6-terra
        may_commit: false
      validation: tests
      prompt: Review the implementation.
```

AgentFlow compiles the mapping-valued actor into an internal named agent and
rewrites the phase to reference it. The generated agent then follows the same
normalization and runtime path as any explicitly named actor. In particular,
`defaults.agent` inheritance still applies, locally written boolean values such
as `may_commit: false` still override defaults, and unknown agent fields remain
invalid.

The generated name uses the reserved `__inline_actor__` prefix and the phase ID
when available. Authors should not declare named agents with that prefix. A
collision fails closed instead of silently changing which capability the phase
references.

Use a named actor when multiple phases share an execution capability, when a
repair policy or other workflow object needs to reference the actor, or when the
capability should have a stable human-facing identity. Use the inline form for a
capability that is genuinely local to one phase.

## Inline shell validation

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

AgentFlow does not currently add `dependsOn` as an authoring alias. Dependency
edges require the ready-node scheduler and concurrent-mutation semantics from
the explicit dependency-graph roadmap work. Accepting the syntax before the
runtime can enforce those semantics would create a misleading contract.

Similarly, concise validation syntax does not add model-decided completion,
`skip`-on-failure acceptance, or unbounded repair. Those behaviors would weaken
the deterministic acceptance boundary rather than merely shorten YAML.
