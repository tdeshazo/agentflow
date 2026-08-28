# Authoring AgentFlow v1alpha1 workflows

This is the practical authoring reference for creating executable
`agentflow.dev/v1alpha1` `AgentWorkflow` documents without inspecting the
AgentFlow implementation source.

Use this file together with `agentflow-v1alpha1.md`. This file answers
"what should I write?"; the field guide answers "what does this field mean?".

## Contents

- [Authoring contract](#authoring-contract)
- [Canonical top level](#canonical-top-level)
- [Recommended section order](#recommended-section-order)
- [Supported executable runtime surface](#supported-executable-runtime-surface)
- [Expressions and conditions](#expressions-and-conditions)
- [Concise defaults pattern](#concise-defaults-pattern)
- [Minimal executable workflow](#minimal-executable-workflow)
- [Robust implementation + audit + completion template](#robust-implementation--audit--completion-template)
- [Criterion-driven template fragment](#criterion-driven-template-fragment)
- [Validation loop](#validation-loop)
- [Common authoring mistakes](#common-authoring-mistakes)
- [Design heuristic](#design-heuristic)

## Authoring contract

Follow these rules:

1. Use only documented fields and values.
2. Prefer the concise authoring layer (`spec.defaults` +
   `spec.lifecycle.policy: safe-resume`) for new workflows.
3. Keep mutation authority, agent capability, deterministic validation, human
   evidence, and completion separate.
4. Every mutable AI phase must resolve to a deterministic validation.
5. Use stable named references instead of duplicating executable definitions.
6. Validate the authored document and inspect its expanded plan before running.
7. If validation says a documented construct is unsupported, redesign around
   the supported surface or report the limitation. Do not inspect source code
   to invent a workaround.

## Canonical top level

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: bounded-workflow
  description: One bounded outcome.

spec:
  # executable workflow contract
```

`metadata.name` is required. Keep it stable because durable workflow state is
namespaced by workflow identity.

## Recommended section order

For new workflows, write sections in this order:

```text
parameters / paths
workspace / state
defaults / agents
tools
preconditions
validation
progress
phases
humanGates
completion
flow
```

This makes references easy to resolve while authoring. Execution order comes
from `flow`, not from YAML declaration order.

## Supported executable runtime surface

The current reference runtime intentionally fails closed. Stay within this
surface when the goal is an executable workflow.

### Parameters

Supported `type` values:

- `string`
- `path`
- `boolean`
- `integer`

Resolution order is:

`--set name=value` → declared `env` → typed `default`

Environment variable names must be valid environment identifiers. Defaults must
have the declared YAML type unless the default is a documented expression that
resolves to that type.

### State/workspace

Supported values when explicitly set:

- `spec.state.backend: git-dir`
- `spec.workspace.vcs: git`
- `spec.temp.cleanup: on-exit`

For mutable workflows, declare `workspace.mutationPolicy.allowed`. Treat it as
an allowlist, not documentation.

Integrity modes:

- `exact-hash`
- `group-exact-hash`
- `normalized-hash`

`normalized-hash` requires `normalize.command`. The other integrity modes must
not declare a normalize command.

Integrity is a list of named rules under `mutationPolicy`, not a single map.
Each rule requires an `id`, a `mode`, and one or more `paths` (which may be
workspace-relative patterns):

```yaml
mutationPolicy:
  allowed: [src/**]
  integrity:
    - id: governance
      mode: exact-hash
      paths: [GOVERNANCE.md, policy/**]
```

Use an integrity rule for protected content even when those paths are outside
the mutation allowlist; the allowlist and integrity rules enforce different
boundaries. Refer to the named rule list from assertions with
`spec.workspace.mutationPolicy.integrity`.

### Agents

Supported runner:

- `codex`

Supported non-empty approval policy:

- `never`

Common capability fields:

```yaml
runner: codex
model: "<model-name-or-expression>"
sandbox: workspace-write
approval: never
ephemeral: true
may_commit: true
output_last_message: true
```

Terminal/provider presentation is runtime-owned. `color` is not an
`AgentWorkflow` field; do not add it under `spec.agents` or
`spec.defaults.agent`.

An agent's ability to write or commit never grants acceptance authority.

### Tools

Supported tool `type` values:

- `shell`
- `workspace-policy`
- `git-checkpoint`
- `file-regex`
- `markdown-checklist-progress`

Typical examples:

```yaml
tools:
  gate:
    type: shell
    command: "./scripts/check.sh"

  assert-scope:
    type: workspace-policy
    policy: spec.workspace.mutationPolicy

  checkpoint:
    type: git-checkpoint
    stage: allowed-dirty-files
    commit_if_dirty: true
    require_clean_after: true

  assert-regex:
    type: file-regex
```

Validation tool invocations have the stable shape:

```yaml
- uses: gate
  if: "{{ parameters.run_gate }}"
  with:
    path: README.md
    regex: '^Status: Complete$'
```

`with` is intentionally narrow; the executable core currently documents
`path` and `regex` arguments for file-regex-style uses. Do not invent arbitrary
tool arguments.

### Preconditions

Supported precondition `type` values:

- `git-repository`
- `commands-exist`
- `files-exist`
- `file-contains`
- `git-object-exists`
- `git-ancestor`
- `git-lineage`
- `git-current-branch-equals`
- `workspace-integrity`

Each precondition needs the fields appropriate to its type. Examples:

```yaml
preconditions:
  - id: repo
    type: git-repository
    path: "{{ parameters.repo_root }}"

  - id: commands
    type: commands-exist
    commands: [git, codex, sh]

  - id: files
    type: files-exist
    paths: [scripts/check.sh]

  - id: canonical-ci
    type: file-contains
    path: .github/workflows/quality.yml
    text: scripts/check.sh
```

When a workflow claims to implement the "next" roadmap item or criterion,
bind that claim before actor execution. Use deterministic `file-contains`
preconditions for the authoritative roadmap order, every required dependency's
completed state, and the exact unchecked target. A prompt that says "next" is
actor guidance, not scheduling evidence.

### Lifecycle

Preferred lifecycle:

```yaml
lifecycle:
  policy: safe-resume
  validation: gate
```

`safe-resume` is the supported lifecycle policy. `checkpoint` may optionally
name an existing git-checkpoint tool. If lifecycle validation is omitted, every
phase using the runtime-owned lifecycle must resolve its own validation.

Do not combine concise `spec.lifecycle` with legacy procedural
`spec.phaseDefaults` or per-phase `after` actions.

### Validation and repair

A named validation must have at least one deterministic step:

```yaml
validation:
  gate:
    steps:
      - uses: canonical-gate
```

Concise failure policies:

```yaml
validation:
  hard-gate:
    repair: none
    steps:
      - uses: canonical-gate

  repairable-gate:
    repair: once
    steps:
      - uses: canonical-gate
```

`repair: once` requires a repair actor either locally or through
`spec.defaults.repair`.

Equivalent explicit one-shot policy:

```yaml
onFailure:
  strategy: repair-once
  maxRepairAttempts: 1
  repair:
    actor: reviewer
    reasoning: high
    prompt: Repair only the bounded deterministic failure.
  then:
    - uses: canonical-gate
  exhausted: fail-workflow
```

Supported failure endpoints are bounded. Do not create unbounded retry loops.

`dependencies` must be non-empty workspace-relative paths or glob patterns
when present.

### Phase kinds

Executable phase kinds:

- `criterion`
- `implementation`
- `audit`
- `bookkeeping`

`tool` and `human` are documented phase concepts but are not executable phase
kinds in the current reference runtime. Use deterministic flow steps and
`humanGates` instead.

Common fields:

```yaml
- id: implement
  kind: implementation
  label: bounded-implementation
  actor: worker
  reasoning: high
  requiresChange: true
  validation: gate
  if: "{{ parameters.enabled }}"
  prompt: |
    Implement only the bounded requested change.
```

A phase must have a stable `id`. An actor can be inherited from
`spec.defaults.phases.<kind>`.

For runtime-owned lifecycle phases, validation can resolve from:

1. `phase.validation`;
2. `spec.defaults.phases.<kind>.validation`;
3. `spec.lifecycle.validation` (or inherited default lifecycle).

At least one must resolve.

### Progress

Supported source:

```yaml
source:
  type: markdown-checklist
```

Supported selection strategy:

```yaml
selection:
  strategy: first-unchecked
```

Prefer stable criteria:

```yaml
progress:
  source:
    type: markdown-checklist
    path: docs/acceptance.md
    uncheckedPattern: '^- \[ \] (.+)$'
    checkedPattern: '^- \[[xX]\] (.+)$'
  selection:
    strategy: first-unchecked
  criteria:
    - id: api
      text: API accepts the new input.
    - id: tests
      text: Regression coverage passes.
  phaseInvariant:
    targeted_item_must_be_checked: true
    unchecked_count_delta: -1
    no_other_criterion_may_close: true
```

New criterion phases should use `criterionID`:

```yaml
- id: implement-api
  kind: criterion
  criterionID: api
  advanceProgress: true
  prompt: Implement only the API criterion. Do not edit the acceptance checklist.
```

With `advanceProgress: true`, the actor must not edit the declared progress
source. The engine owns the exact checkbox transition after deterministic
acceptance.

### Bookkeeping

Engine-owned bookkeeping phases have no actor.

Supported transition types:

- `markdown-checklist`
- `markdown-index`
- `markdown-status`

Examples:

```yaml
- id: mark-index
  kind: bookkeeping
  validation: gate
  bookkeeping:
    - type: markdown-index
      path: docs/README.md
      item: "Release complete"
      state: checked
```

```yaml
- id: set-status
  kind: bookkeeping
  validation: gate
  bookkeeping:
    - type: markdown-status
      path: docs/release.md
      label: "Status"
      from: "In progress"
      to: "Complete"
```

Checklist/index states are `checked` or `unchecked`. Status transitions require
different non-empty `from` and `to` values.

### Human gates

Use `humanGates` rather than a `human` phase.

Example:

```yaml
humanGates:
  - id: review
    requires: [audit]
    instructions: >-
      Inspect the final checkout and verify the requested behavior manually.
    checklist:
      - id: behavior
        text: The requested behavior works in the target environment.
      - id: scope
        text: The change remains within the declared scope.
    acknowledgement:
      type: exact-text
      value: "yes"
    evidence:
      value: head_commit
```

A gate may use `when` or `if`, but not both. False conditional gates do not
prompt and record their declared durable evidence at the current commit.

### Flow

Each flow step must contain one executable action (or `then` actions).

Common flow forms:

```yaml
flow:
  - validate: starting-state
  - phase: implement
  - phase: audit
  - human: review
  - complete: done
```

Other supported step forms include `checkpoint`, `assert`, `recover:
activePhase`, a bounded progress `loop`, and conditional `then` report/stop
actions.

Do not use declaration order as a substitute for `flow`.

Dynamic checklist loop:

```yaml
- loop:
    while: "{{ progress.unchecked_count > 0 }}"
    maxIterations: "{{ parameters.max_dynamic_steps }}"
    select: "{{ progress.next_unchecked }}"
    dispatchByCriterion:
      api: implement-api
      tests: implement-tests
    requireUncheckedCountDelta: -1
```

This is the only dynamic loop form. Dispatch keys should be stable criterion
IDs, every target must be a declared phase, and the required count delta must
be negative.

### Completion

Supported assertion `type` values:

- `progress-empty`
- `workspace-integrity`
- `integrity-baseline-unchanged`
- `implementation-workspace-clean`

Assertions may also delegate to a named tool with `uses`.

Recommended durable completion:

```yaml
completion:
  done:
    assertions:
      - type: workspace-integrity
        policy: spec.workspace.mutationPolicy.integrity
    finalValidation: final-gate
    afterCheckpointAssertions:
      - type: workspace-integrity
        policy: spec.workspace.mutationPolicy.integrity
      - type: implementation-workspace-clean
    writeMarker:
      value: head_commit
    summary:
      title: "Workflow complete."
      include:
        - branch
        - base_commit
        - head_commit
        - commits_since_base
        - changed_files_since_base
        - workspace_clean
```

If a workflow has an explicit checkpoint tool and wants completion to call it:

```yaml
checkpoint:
  uses: checkpoint
  label: final-checkpoint
```

The complete marker should be the last durable state transition.

`finalValidation` must re-prove the workflow's semantic outcome with the
canonical deterministic gate. Scope, formatting, cleanliness, and diff checks
are complementary safety checks, not terminal acceptance by themselves. In
particular, a command such as `git diff --check` that inspects only the current
worktree is vacuous after safe-resume has checkpointed a clean tree.

When a workflow owns one criterion within a larger checklist, `progress-empty`
is intentionally too broad. Add deterministic completion evidence for the
exact owned item, such as a `file-regex` validation step matching its checked
form, so durable completion cannot be written for an unadvanced target.

## Expressions and conditions

`{{ ... }}` is a constrained expression language, not a general template
engine.

Common supported references:

- `parameters.<name>`
- `spec.paths.<name>`
- `metadata.name`
- `workflow.file`
- `env.<NAME>` (commonly with `| default('value')`)
- documented `state.*` and active-phase values
- `phase.id`, `phase.label`, `phase.kind`, `phase.criterion`,
  `phase.requiresChange`
- `progress.unchecked_count`
- `progress.next_unchecked`
- `progress.is_checked(<criterion>)`
- `head_commit`
- `validation.failure.log`
- `tail(validation.failure.log, <positive integer>)`

Supported boolean/comparison forms include:

- `not`
- `and`
- `or`
- `==`
- `!=`
- integer comparisons
- parentheses

Conditions must evaluate to booleans. Unknown roots, unsupported functions,
malformed syntax, incompatible comparisons, and unavailable runtime values fail
closed.

Do not place shell interpolation or a general programming language inside
expressions.

## Concise defaults pattern

Use defaults when several actors/phases share capability values:

```yaml
defaults:
  agent:
    runner: codex
    sandbox: workspace-write
    approval: never
    ephemeral: true
    may_commit: true
  lifecycle:
    policy: safe-resume
    validation: gate
  phases:
    implementation:
      actor: worker
      reasoning: high
      requiresChange: true
    audit:
      actor: reviewer
      reasoning: high
      requiresChange: false
  repair:
    actor: reviewer
    reasoning: high
    prompt: |
      Repair only the bounded deterministic validation failure.
```

Local agent/phase fields override inherited values, including explicit boolean
`false`.

Defaults do not grant mutation authority and cannot replace tools, validation
steps, flow, human gates, or completion.

## Minimal executable workflow

Use this as a starting point for a single bounded implementation phase:

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: bounded-change
  description: Implement one bounded repository change and prove it deterministically.

spec:
  parameters:
    repo_root:
      type: path
      default: "."

  workspace:
    root: "{{ parameters.repo_root }}"
    vcs: git
    mutationPolicy:
      allowed:
        - internal/**
        - README.md

  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: true
    lifecycle:
      policy: safe-resume
      validation: gate
    phases:
      implementation:
        actor: worker
        reasoning: high
        requiresChange: true

  agents:
    worker: {}

  tools:
    canonical-gate:
      type: shell
      command: "./scripts/check.sh"

  preconditions:
    - id: repository
      type: git-repository
      path: "{{ parameters.repo_root }}"
    - id: required-tools
      type: commands-exist
      commands: [git, codex, sh]
    - id: gate-file
      type: files-exist
      paths: [scripts/check.sh]

  validation:
    gate:
      steps:
        - uses: canonical-gate

  phases:
    - id: implement
      kind: implementation
      label: bounded-change
      prompt: |
        Implement only the requested bounded change.
        Keep edits within the declared mutation allowlist.
        Run the repository gate before returning.
        Do not weaken validation or change protected policy.

  flow:
    - phase: implement
```

Replace paths and the canonical gate with repository facts supplied by the user
or discovered from the target repository. Do not fabricate them.

## Robust implementation + audit + completion template

Use this pattern when the workflow should create durable completion evidence:

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: bounded-feature

spec:
  parameters:
    repo_root:
      type: path
      default: "."
    task:
      type: string
      default: ""

  workspace:
    root: "{{ parameters.repo_root }}"
    vcs: git
    cleanliness:
      before_first_run: required
      outside_recoverable_active_phase: required
      after_checkpoint: required
    mutationPolicy:
      allowed:
        - internal/**
        - cmd/**
        - README.md

  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: true
    lifecycle:
      policy: safe-resume
      validation: gate
    phases:
      implementation:
        actor: implementer
        reasoning: high
        requiresChange: true
      audit:
        actor: reviewer
        reasoning: high
        requiresChange: false
    repair:
      actor: reviewer
      reasoning: high
      prompt: |
        Repair only the current bounded validation failure.
        Do not broaden mutation scope or weaken the gate.

  agents:
    implementer: {}
    reviewer: {}

  tools:
    canonical-gate:
      type: shell
      command: "./scripts/check.sh"

  validation:
    gate:
      repair: once
      steps:
        - uses: canonical-gate
    hard-final:
      repair: none
      steps:
        - uses: canonical-gate

  phases:
    - id: implement
      kind: implementation
      prompt: |
        Implement exactly this bounded task:

        {{ parameters.task }}

        Work only within the mutation allowlist and add focused tests.
        Run the canonical gate before returning.

    - id: audit
      kind: audit
      validation: hard-final
      prompt: |
        Independently inspect the current checkout for the bounded task.
        Repair a genuine in-scope defect if needed. Otherwise leave it unchanged.
        Do not accept the prior agent's completion claim as evidence.

  completion:
    done:
      finalValidation: hard-final
      afterCheckpointAssertions:
        - type: implementation-workspace-clean
      writeMarker:
        value: head_commit
      summary:
        title: "Bounded workflow complete."
        include: [branch, base_commit, head_commit, changed_files_since_base]

  flow:
    - phase: implement
    - phase: audit
    - complete: done
```

If an empty task must be rejected before actor execution, add a conditional flow
step that stops with a useful message.

## Criterion-driven template fragment

Add this when the source of truth is a Markdown checklist:

```yaml
progress:
  source:
    type: markdown-checklist
    path: docs/acceptance.md
    uncheckedPattern: '^- \[ \] (.+)$'
    checkedPattern: '^- \[[xX]\] (.+)$'
  selection:
    strategy: first-unchecked
  criteria:
    - id: first
      text: First exact checklist item
    - id: second
      text: Second exact checklist item
  phaseInvariant:
    targeted_item_must_be_checked: true
    unchecked_count_delta: -1
    no_other_criterion_may_close: true

phases:
  - id: first
    kind: criterion
    actor: worker
    criterionID: first
    advanceProgress: true
    validation: gate
    requiresChange: true
    prompt: |
      Implement only criterion "first".
      Do not edit docs/acceptance.md; AgentFlow owns the progress transition.

  - id: second
    kind: criterion
    actor: worker
    criterionID: second
    advanceProgress: true
    validation: gate
    requiresChange: true
    prompt: |
      Implement only criterion "second".
      Do not edit docs/acceptance.md; AgentFlow owns the progress transition.

flow:
  - phase: first
  - phase: second
```

For dynamic dispatch, replace the explicit phase steps with the bounded loop
shown above.

## Validation loop

The CLI is part of the public authoring contract. Use it instead of reading
implementation source.

Installed CLI:

```sh
agentflow validate -f workflow.yaml
agentflow plan --expanded -f workflow.yaml
```

From an AgentFlow source checkout:

```sh
go run . validate -f workflow.yaml
go run . plan --expanded -f workflow.yaml
```

`validate` performs document-only checks and reports invalid versus
valid-but-unsupported constructs without opening a target repository or calling
an actor.

`plan --expanded` validates the authored document, normalizes concise defaults,
and prints the resolved executable representation without opening a repository
or invoking actors or mutable tools.

Authoring loop:

1. Write the smallest workflow that expresses the requested control contract.
2. Run `validate`.
3. Fix every invalid diagnostic at the reported YAML path.
4. Remove or replace unsupported runtime constructs when execution is required.
5. Run `plan --expanded`.
6. Inspect resolved actor, lifecycle, validation, repair, mutation/progress,
   checkpoint, human-gate, and completion semantics.
7. Confirm every deterministic command is meaningful at the point where the
   lifecycle runs it; account for phase checkpoints and clean-tree boundaries.
8. Confirm terminal validation re-runs semantic acceptance and proves any
   exact progress item the workflow owns.
9. Only then consider `run`.

Do not use live workflow execution as schema discovery.

## Common authoring mistakes

Avoid these:

- inventing a tool/precondition/assertion type;
- using `tool` or `human` as an executable phase in the current runtime;
- declaring a mutable AI phase without a resolved deterministic validation;
- combining concise lifecycle with legacy phase lifecycle actions;
- putting acceptance authority in an actor prompt;
- claiming a roadmap item is next without preconditions that bind roadmap
  order, dependency completion, and the pending target;
- broadening `mutationPolicy.allowed` just to make validation pass;
- relying on scope checks to protect ignored local workflows, instructions, or
  skills that affect actor behavior instead of adding explicit integrity rules;
- letting an actor edit engine-owned progress;
- using `criterion` display text when stable `criterionID` is available;
- declaring both `when` and `if` on one human gate;
- using `repair: once` without a resolvable repair actor;
- making a repair gate the final hard acceptance gate when repair is not desired;
- writing a completion marker before final validation/checkpoint/post-checks;
- using a worktree-only diff check after checkpoint as meaningful completion
  evidence;
- making scope, formatting, cleanliness, or diff-only checks the terminal gate
  without re-running the canonical semantic validation;
- completing a single-criterion workflow without proving that exact checklist
  item is checked;
- using a human gate for something a deterministic command can prove;
- authoring `color` or other terminal/provider presentation policy in a workflow;
- using undocumented template functions or general shell syntax in expressions;
- copying fields from old examples without checking the current concise
  lifecycle pattern;
- reading implementation source to work around a public validation diagnostic.

## Design heuristic

When uncertain, prefer the design with:

- fewer actors;
- fewer phases;
- one canonical deterministic gate;
- explicit mutation scope;
- safe-resume lifecycle;
- bounded repair;
- stable IDs;
- engine-owned progress transitions;
- human verification only where automation cannot decide;
- a separate durable completion transition only when the use case needs it.

Complexity should be justified by orchestration semantics, not by the amount of
work in the actor prompt.
