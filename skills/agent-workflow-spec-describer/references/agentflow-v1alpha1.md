# AgentWorkflow v1alpha1 field guide

This reference describes the semantics used by the workflow definitions in this conversation. It is a descriptive guide for agents, not a claim of an externally standardized engine implementation.

## Top level

- `apiVersion`: DSL version. Current examples use `agentflow.dev/v1alpha1`.
- `kind`: `AgentWorkflow`.
- `metadata`: workflow identity, description, and optional source provenance.
- `spec`: executable orchestration contract.

## `spec.parameters`

Runtime inputs and environment-backed overrides. Parameters may alter models, repository root, human verification, reset behavior, or iteration bounds.

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

An active phase can store its phase id, start commit, and progress snapshot. Recovery may preserve partial commits and working-tree changes.

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

## `spec.validation`

Named deterministic acceptance gates.

A validation can contain ordered steps and a failure policy.

Common policies:

- `repair: none`: hard failure; stop without agent repair.
- `repair-once`: invoke one repair actor, then rerun deterministic checks once.
- exhausted repair budget: fail workflow.

Do not assume every named gate shares the same repair policy.

## `spec.lifecycle`

`policy: safe-resume` is the concise lifecycle for mutable AI phases. The
runtime owns clean phase boundaries, phase-start capture, durable active and
actor-completion state, deterministic acceptance, accepted-work checkpointing,
completed-phase evidence, and active-state cleanup. `validation` supplies the
default deterministic gate; `phase.validation` selects a phase-specific gate.
Safety properties cannot be disabled by lifecycle fields.

## `spec.phaseDefaults`

Legacy procedural lifecycle inherited by phases. It remains valid for existing
v1alpha1 documents; new concise workflows should use `spec.lifecycle`.

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

Skip behavior may use completed markers or already-checked criteria. Review skip semantics carefully because they can bypass validation if configured loosely.

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

## `spec.recovery`

Recovery of an interrupted active phase.

A strong recovery contract (also derived automatically by safe-resume) should:

1. reads durable active-phase state;
2. restores the same phase definition;
3. validates saved phase-start lineage;
4. checks whether criterion progress may already have completed;
5. preflights retained partial commits and worktree changes before rerunning an
   actor;
6. preserves useful commits/working-tree changes;
7. reruns or repairs only the same phase when needed;
8. applies the normal after-phase validation/checkpoint/marker path.

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

`flow` can also express dynamic loops for “next unchecked criterion” workflows.

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
