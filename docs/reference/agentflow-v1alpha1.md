# AgentFlow v1alpha1 field guide

This reference describes the semantics used by the workflow definitions in this conversation. It is a descriptive guide for agents, not a claim of an externally standardized engine implementation.

## Top level

- `apiVersion`: DSL version. Current examples use `agentflow.dev/v1alpha1`.
- `kind`: `AgentWorkflow`.
- `metadata`: workflow identity, description, and optional source provenance.
- `spec`: executable orchestration contract.

## `spec.parameters`

Runtime inputs and environment-backed overrides. Parameters may alter models, repository root, human verification, reset behavior, or iteration bounds.

Parameters are typed as `string`, `path`, `boolean`, or `integer`. Resolution is
deterministic: a `--set name=value` override wins over a declared `env` value,
which wins over `default`. Every resulting value must coerce to its declared
type; unknown overrides, malformed values, and cyclic parameter defaults fail
before the engine opens a repository. A default may reference another declared
parameter, including one declared later in YAML.

`-C` is a workspace-root override, not an implicit parameter override. This
keeps the command-line repository target independent from a workflow's own
parameter names.

## Expressions and conditions

`{{ ... }}` uses a small expression language, not a general-purpose template
or programming language. Supported values are booleans, integers, quoted
strings, and these references:

- `parameters.<name>`, `spec.paths.<name>`, `metadata.name`, and `workflow.file`;
- `env.<NAME>` (normally with `| default('value')`);
- documented `state.*` and active-phase fields;
- `phase.id`, `phase.label`, `phase.kind`, `phase.criterion`, and
  `phase.requiresChange`;
- `progress.unchecked_count`, `progress.next_unchecked`, and
  `progress.is_checked(<criterion>)`; and
- `head_commit` and `validation.failure.log`, with
  `tail(validation.failure.log, <positive integer>)` for bounded log context.

Expressions may use `not`, `and`, `or`, `==`, `!=`, integer comparisons, and
parentheses. Conditions must evaluate to a boolean; strings such as `"true"`
are not silently coerced. Template interpolation turns values into text only
for an ordinary string field. Typed fields (conditions, loop bounds, and typed
parameter defaults) preserve and check their values.

Unknown references, unsupported functions, malformed syntax, incompatible
comparisons, and unavailable runtime values fail closed. They are never treated
as an empty string or a false condition. A new expression form is therefore an
explicit specification/runtime change rather than a string-substitution case.

## `spec.paths`

Named repository paths used elsewhere by templates. This is convenience/indirection, not authority by itself.

## `spec.state`

Persistent orchestration state.

Important concepts:

- `backend`: persistence mechanism, commonly state under `.git/`.
- `directory`: workflow-specific durable state location.
- `records.base_commit`: repository commit at workflow initialization.
- `records.branch`: named branch captured at initialization.
- `records.active_phase`: interrupted/in-progress phase record.
- completed phase marker(s): durable evidence that a phase reached a checkpoint.
- human confirmation record: durable human-gate evidence.
- workflow complete record: terminal completion marker.
- integrity records: baseline hashes captured during initialization.

### Initialization

May require a clean implementation workspace and named branch before capturing base commit, branch, and integrity baselines.

### Reset

Deletes workflow state only under declared clean-state conditions. Reset abandons orchestration history; it should not silently normalize unrelated dirty changes.

### Lineage

Typical safety invariants:

- saved base commit still exists;
- saved base remains an ancestor of `HEAD`;
- current branch still equals the workflow branch.

### Resume

Completed phase markers are commonly valid only if their recorded commits exist and remain ancestors of current `HEAD`.

An active phase can store its phase id, start commit, progress snapshot, and a
durable `actor_completed` flag. The runtime writes that flag immediately after
the primary phase actor returns successfully, before validation or checkpoint
work begins. Recovery may preserve partial commits and working-tree changes,
but may only attempt deterministic acceptance when this flag is true.
Deterministic validation failures are classified in the same runtime record as
either `validation` (which may use its configured bounded repair policy) or
`safety` (which is terminal and never authorizes further actor work).

## `spec.workspace`

Defines mutation authority and Git cleanliness/checkpoint rules.

### Cleanliness

Can require clean state:

- before first run;
- before each new phase;
- outside a recoverable active phase;
- after checkpoint.

### Mutation policy

`mutationPolicy.allowed` is an allowlist for implementation changes. Anything outside it should fail scope validation unless explicitly ignored as a local control file.

### Integrity modes

- `exact-hash`: file contents must remain byte/identity equivalent to baseline.
- `group-exact-hash`: a tracked file group must retain the same combined content identity.
- `normalized-hash`: a normalization transform removes explicitly permitted bookkeeping differences before hashing; all other structure remains protected.

Integrity rules and path allowlists solve different problems: an allowed path may still have restricted semantics through normalized integrity checks.

### Checkpointing

May permit agent-created commits and/or automatically checkpoint successful uncommitted work. A strong checkpoint policy asserts scope before staging, stages only allowed dirty files, commits if needed, requires a clean tree afterward, and reasserts scope.

## `spec.agents`

Named AI execution capabilities. Typical fields select runner, model, sandbox, approval policy, ephemeral context, and commit permission.

Agent capability does not determine phase acceptance. Validation does.

## `spec.tools`

Named deterministic or orchestration-native operations.

Examples:

- workspace scope assertion;
- shell quality gate;
- Git checkpoint;
- regex assertion;
- progress assertion.

A `type` declares intended behavior; exact runtime implementation depends on the workflow engine.

## `spec.preconditions`

Deterministic checks performed before mutable work. Common checks include repository identity, required commands/files, delegation from CI workflow to canonical gate, saved Git lineage, and protected-boundary integrity.

## `spec.progress`

Defines external completion progress, often a Markdown acceptance checklist.

Important concepts:

- source path and checked/unchecked patterns;
- named criteria;
- targeted criterion per phase;
- invariant that exactly one intended criterion closes.

`unchecked_count_delta: -1` means a criterion phase must reduce the unchecked count by exactly one. Combined with `targeted_item_must_be_checked`, it prevents an agent from closing unrelated criteria to manufacture progress.

With `selection.strategy: first-unchecked`, the runtime reads the declared
checklist source in document order. `progress.next_unchecked` is that one item,
not an arbitrary query over repository files. If `no_other_criterion_may_close`
is set, the accepted phase must not newly check any other item as it closes its
target.

## `spec.validation`

Named deterministic acceptance gates.

A validation can contain ordered steps and a failure policy.

Common policies:

- `repair: none`: hard failure; stop without agent repair.
- `repair-once`: invoke one repair actor, then rerun deterministic checks once.
  If `onFailure.then` is omitted, the original ordered validation steps are
  rerun; an omitted list never turns a failed gate into success.
- exhausted repair budget: fail workflow.

Do not assume every named gate shares the same repair policy.

## `spec.lifecycle`

`lifecycle` is the concise contract for mutable AI phases. The safe policy is
selected with `policy: safe-resume`; it is also the runtime default for a phase
that has no legacy procedural lifecycle actions. `validation` names the
deterministic gate used for phases that do not set `phase.validation`.

Under safe resume, the runtime owns the clean phase boundary, phase-start commit
and progress capture, durable active-phase and actor-completion state,
deterministic acceptance, checkpointing of accepted work, the commit-valued
completed-phase marker, and active-state cleanup. A named `checkpoint` is an
optional existing tool override; omitting it uses the runtime Git checkpoint.
The safety properties cannot be disabled by lifecycle fields.

`phase.validation` overrides the lifecycle's default validation for one phase.
The actor's successful return is never acceptance evidence: a phase must still
pass its deterministic gate, progress/net-change rules where applicable, scope,
integrity, lineage, and checkpoint postconditions.

## `spec.phaseDefaults`

Legacy procedural lifecycle actions inherited by phases. New workflows should
prefer `spec.lifecycle`; this section remains executable so existing v1alpha1
documents, including the bootstrap workflow, retain their original meaning.

Typical `before` operations:

1. require a clean implementation workspace;
2. capture phase start commit;
3. capture progress snapshot;
4. persist active-phase state.

Typical `after` operations:

1. deterministic phase validation;
2. criterion progress assertion when applicable;
3. checkpoint;
4. net-change assertion when `requiresChange` is true;
5. write completed-phase marker at current `HEAD`;
6. clear active-phase state.

The phase actor's successful return is persisted before these operations. An
interruption before that evidence reruns the same actor against the retained
repository state; an interruption after it resumes the normal deterministic
acceptance sequence without replaying the actor. A completed phase marker is
still authoritative even if the process stopped before active state was
cleared.

Skip behavior may use completed markers or already-checked criteria. The
reference runtime does not permit an acceptance bypass: an accepted phase marker
requires a successful validation in the current lifecycle attempt, and an
already-checked criterion must validate before it receives a marker.

## `spec.phases`

Bounded units of AI work.

Common kinds:

- `criterion`: closes one acceptance criterion and is progress-constrained.
- `implementation`: bounded code or configuration change not directly tied to a checklist item.
- `audit`: cross-cutting review/repair; may legitimately produce no diff.
- `bookkeeping`: authorized completion metadata only.
- `tool`: deterministic operation.
- `human`: human-owned verification when represented as a phase rather than `humanGates`.

Key fields:

- `id`: stable phase identity.
- `label`: human-readable/log identity.
- `actor`: named AI capability.
- `reasoning`: requested effort tier.
- `criterion`: targeted progress item.
- `requiresChange`: whether a net repository diff since phase start is mandatory.
- `validation`: optional deterministic gate override for `spec.lifecycle`.
- `if`: optional boolean condition. A false condition skips the phase without
  invoking its actor or creating a completion marker.
- `prompt`: bounded work instructions.

## `spec.humanGates`

Manual verification as a durable workflow gate.

Important fields:

- `after` / flow placement: when verification occurs;
- `when`: whether human verification is required;
- `instructions`: environment/procedure;
- `checklist`: behaviors to verify;
- `acknowledgement`: exact confirmation protocol;
- `evidence`: persistent record, commonly current `HEAD`;
- `skip`: explicit permitted bypass and evidence when verification is disabled.

`when` (or the equivalent `if` spelling where supported) is a boolean
condition. A false gate does not prompt; it records the declared durable gate
evidence at the current commit.

## `spec.recovery`

Explicit legacy recovery escape hatch for an interrupted active phase. Normal
safe recovery is runtime-derived from the durable active-phase record and
`spec.lifecycle`; it does not require a `flow` recovery step or a procedural
`activePhase` sequence. Existing `spec.recovery` documents remain valid, but an
override cannot mark a phase complete without the same deterministic acceptance
and checkpoint contract.

A strong recovery contract:

1. reads durable active-phase state;
2. restores the same phase definition;
3. validates saved phase-start lineage;
4. treats a valid completed phase marker as accepted and clears only stale active state;
5. reruns the same actor when `actor_completed` is absent, preserving partial work;
6. resumes deterministic validation/checkpoint/marker work without replaying the actor when `actor_completed` is present;
7. preflights retained commits and working-tree changes before rerunning an actor,
   preserves them, and never deletes them as a recovery side effect;
8. keeps deterministic validation repair budgets and repository-policy failures bounded or terminal as configured.

## `spec.flow`

Runtime control order. Prefer this section over declaration order when explaining execution.

Typical flow:

1. stop successfully if already complete;
2. recover active interrupted phase;
3. require clean post-recovery state;
4. validate starting state;
5. run criterion phases;
6. assert criteria closed;
7. run audits;
8. run post-audit hard gate/checkpoint;
9. require/record human verification;
10. run completion bookkeeping;
11. execute completion contract.

`flow` can also express dynamic loops for “next unchecked criterion” workflows:

```yaml
- loop:
    while: "{{ progress.unchecked_count > 0 }}"
    maxIterations: "{{ parameters.max_dynamic_steps }}"
    select: "{{ progress.next_unchecked }}"
    dispatchByCriterion:
      "First acceptance criterion": "01"
      "Second acceptance criterion": "02"
    requireUncheckedCountDelta: -1
```

This is the sole dynamic loop form in `v1alpha1`. `maxIterations` must be a
positive integer and the required delta must be negative. Each selected text
must map to a declared criterion phase targeting that same text. The engine
checks the progress delta after every iteration and fails at the bound rather
than continuing indefinitely. Flow steps, validation tool uses, phase
lifecycle actions, phases, and gates can all carry a boolean condition.

## `spec.completion`

Terminal transition to workflow-complete state.

A robust completion contract commonly requires:

- no remaining acceptance criteria;
- completion status/index regex assertions;
- scope assertion;
- integrity baseline unchanged;
- a hard final canonical validation;
- a final checkpoint;
- post-checkpoint scope/integrity/cleanliness assertions;
- write of durable complete marker at current `HEAD`;
- summary of branch/base/head/commits/changed files/gate status.

The complete marker should be written last, after every required assertion and checkpoint.

## Operational invariants

The examples embody these general invariants:

1. Agents may mutate; deterministic validation authorizes advancement.
2. Allowed paths do not imply unrestricted semantic changes.
3. Every accepted mutable phase ends at a durable Git checkpoint.
4. Repair budgets are bounded and gate-specific.
5. Resume preserves useful work and validates lineage.
6. Human verification is durable state, not prose in an agent prompt.
7. Completion bookkeeping occurs only after implementation/audit/human prerequisites.
8. Workflow completion is a separate validated state transition.
