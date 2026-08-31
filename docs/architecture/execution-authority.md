# Execution authority

AgentFlow keeps three concerns distinct:

- **Workflow definition** is the declarative `AgentWorkflow` document: actors,
  phases and flow, workspace policy, deterministic tools and validations,
  state/recovery policy, human gates, and completion conditions. The document
  describes the contract before a run starts; it is not an execution trace.
- **Execution** is the Go interpreter applying that contract to a Git
  workspace. It resolves inputs, validates the supported runtime surface,
  invokes actors/providers, runs tools, checkpoints accepted work, and records
  durable state.
- **Assurance** is the deterministic evidence that permits advancement:
  deterministic validation authorizes advancement through scope and integrity
  checks, workflow validations, progress assertions, checkpoint postconditions,
  human-gate evidence where required, and final completion assertions. Agent
  output is not acceptance evidence by itself.

## Authority boundaries

An agent has bounded mutation authority. Its provider invocation may change
files and create commits only within the workflow's allowed workspace policy
and that invocation's permissions. The agent may implement or repair work, but
it cannot mark its own phase accepted, waive a protected boundary, extend a
repair budget, or write the workflow-complete transition.

### Shared Agent capability contract

The effective permission for an actor-created commit is the disjunction of the
three existing shared authority sources:

```text
effectiveActorCommitPermission(agent) =
    agent.MayCommit
    OR spec.workspace.agent_commits.allowed
    OR spec.workspace.checkpointing.agent_commits_allowed
```

`Agent.MayCommit` is authored as `may_commit` and is scoped to the specifically
named actor invocation. The two workspace fields are workflow authority: they
authorize actor-created commits for the workflow, independently of whether
another actor has `MayCommit: true`. No actor may borrow another actor's
`MayCommit` value. The invocation boundary and the checkpoint/acceptance
boundary must use this same rule.

The rule applies independently to the primary phase actor, a validation-repair
actor, an actor rerun during recovery, a repair actor invoked by
`completion.validation`, and every future actor invocation through the shared
runtime. It answers only whether that invocation may move repository `HEAD` by
creating commits; it does not authorize workspace paths, integrity changes,
validation, acceptance, dependency release, or completion.

An actor-created commit when all applicable sources are false is a
repository-policy safety failure. It is terminal for the current run: it does
not become a repair invitation, is not accepted because another actor has
`may_commit: true`, is not hidden by later successful validation, cannot satisfy
`dependsOn`, and cannot authorize completion.

Runtime-owned checkpoints are distinct from actor-created commits. An
invocation with effective actor commit permission false still permits AgentFlow
to checkpoint validated, allowed dirty work when the checkpoint contract allows
it. That runtime commit does not consume actor commit authority or change the
invocation that was authorized.

`output_last_message` is provider-neutral capture intent. When true, the
runtime asks the provider to capture and return its final message when the
provider supports that capability. When false, the runtime does not request
provider final-message capture. A returned final message is diagnostic or
presentation output only. It is never deterministic validation evidence,
`actor_completed` evidence, dependency evidence, or completion authority.

Deterministic tools have operational authority for the checks they implement.
The interpreter invokes shell gates, workspace-policy assertions, Git
checkpointing, file/checklist assertions, and other supported tools according
to the workflow. A provider executes the AI actor request; it does not become a
deterministic tool and its final message does not replace a validation result.

### Derived invocation context

Provider-visible context is compiled immediately before each actor invocation;
it is neither authored workflow authority nor durable acceptance state. The
compiler selects the expanded objective, current relevant workspace projection,
accepted direct dependency commits, only the typed artifacts/evidence declared
by the consumer, effective mutation and commit authority, executor capabilities,
required validations, and the selected bounded/redacted repair failure when
applicable. It excludes transcripts, provider output, unrelated contracts,
broad history, artifact bodies, timestamps, random quarantine paths, complete
environments, and secrets.

The engine represents the workspace with a stable placeholder. Provider
adapters may validate and render the structured context and resolve that
placeholder to the isolated actor workspace; they may not reconstruct workflow
semantics or substitute the authoritative workspace. Pending invocation state
continues to persist only attribution and quarantine-reconciliation data, not
the compiled context or objective.

## Acceptance and checkpointing

A mutable phase is accepted only after its configured deterministic validation
actually passes. Where required, progress assertions and net-change rules
also pass. Repair is bounded by the validation's declared policy; a failed
repository-policy or integrity safety check is terminal rather than an
invitation to let an agent override the boundary.

For a criterion phase declaring `criterionID` and `advanceProgress: true`, the
actor implements the work but cannot close its own checklist item: the runtime
records the pre-actor progress snapshot, rejects any actor-authored progress
change, and advances only the declared target after the deterministic gate.
Fully declarative Markdown checklist, status, and index transitions use the
same engine authority on actor-less bookkeeping phases.

After acceptance, checkpointing may preserve a permitted agent-created commit
or commit allowed dirty files as a runtime-owned checkpoint. The checkpoint
asserts scope, stages only allowed files, requires a clean worktree, and
reasserts the policy. The completed-phase marker is written at the accepted
`HEAD`; completion bookkeeping is a separate validated transition after all
phases and required gates pass.

For concise workflows, `spec.lifecycle.policy: safe-resume` makes these phase
boundaries runtime-owned. The runtime also rechecks lineage, protected
integrity, and allowed scope before and after actor/tool work, at checkpoint and
acceptance, and before reusing a completed marker. A lifecycle or legacy
recovery override may change procedural detail only; it cannot bypass the
deterministic acceptance boundary.

### Shared `Completion.FinalValidation` durability contract

The shared `Completion` model exposes `FinalValidation`. In v1alpha1 this is
the `spec.completion.<name>.finalValidation` field; v1alpha2's
`spec.completion.validation` normalizes to the same field on the default
completion. The field always denotes a completion transition, not a general
request to run a named validation.

The logical durable scope of a final validation is:

```text
completion/<completion-name>/<validation-name>
```

The scope is part of the durable identity of every final-gate artifact,
including successful validation evidence, failed-validation state, consumed
repair budget, and pending repair-invocation attribution. It is distinct from
the following transitions even when they use the same validation name:

| Transition | Durable identity |
| --- | --- |
| `Completion.FinalValidation` | `completion/<completion-name>/<validation-name>` |
| ordinary standalone `flow.validate` | standalone validation scope, `<validation-name>` |
| phase validation | that phase's acceptance context and phase record |
| another completion's final validation | that completion's own `completion/<name>/...` scope |

If a backend encodes a logical scope in a ref or record name, it must encode
the complete scope string. Omitting `completion/<completion-name>/` is not a
compatible representation of a final-gate record.

For a `repair-once` final gate, the runtime must durably increment the scoped
repair budget before invoking the repair actor. It then reruns the same
deterministic final validation. The actor's return, output, or commit is never
completion evidence. If revalidation succeeds, the consumed budget remains
until the completion marker and all preceding completion evidence are durable;
only then may the transient scoped repair-budget record be removed. A crash in
the interval after successful revalidation and before the marker therefore
cannot create a fresh repair attempt. On restart, a passing deterministic final
gate continues toward completion with the budget still consumed; a failing
gate fails with exhausted repair budget.

Final validation is necessary but not sufficient for completion. Completion
also requires every configured completion assertion, checkpoint and
post-checkpoint requirement, mutation/scope and lineage boundary, integrity
and cleanliness boundary, and the durable workflow-complete marker. A
consumed repair budget is execution-policy state, never success evidence.

A safety failure in a completion-scoped record is terminal for the run. It is
not repairable, is never downgraded to an ordinary validation failure, and is
not cleared by repair-state migration, a changed `HEAD`, later validation
success, or an unscoped record. It remains authoritative until explicit reset
or abandon state disposal.

#### Pre-upgrade v1alpha1 compatibility

Before this contract, a v1alpha1 completion final validation may have written
legacy standalone records under the validation name. Such a record is
ambiguous: it may have come from an ordinary flow validation or from the
completion transition. When a v1alpha1 completion final gate is opened, a
well-formed legacy record for the same validation name must therefore be
recognized conservatively before the runtime decides whether repair is
available. A legacy repair budget is copied to the completion scope without
lowering its consumed attempt count. A legacy safety failure remains directly
authoritative in its original record; it is neither cleared nor downgraded.

If both legacy and scoped records exist, safety state wins and repair attempts
are combined conservatively (the greater consumed count is retained). A
malformed, conflicting, or unclassifiable legacy record fails closed; the
scoped record must not be treated as fresh. The legacy source is not deleted
during migration. Safety remains terminal in its legacy record, and a migrated
repair-budget source is removable only through explicit state disposal (or
post-completion cleanup that cannot remove safety state). New v1alpha1
standalone flow validations remain outside this completion contract; only
pre-upgrade ambiguous records receive the conservative completion lookup.

## Durable execution and recovery

The interpreter stores workflow evidence in Git objects and namespaced refs,
including base/branch lineage, an active-phase record, completed phase markers,
human-gate evidence, integrity baselines, run identity, a pending actor
invocation record, and the final completion marker. Successful deterministic
validations also have digest-only, content-addressed evidence keyed by their
definition, resolved inputs, declared dependencies, relevant file contents,
policy, run identity, and acceptance context. A lookup still performs the
safety boundary first, so this evidence survives process interruption without
becoming authority across a changed tree, lineage, integrity boundary, scope,
or repair policy. These records survive process interruption without a separate
state database and are tied to repository commits or current workspace identity
as appropriate.

Before any provider invocation, the runtime durably writes a versioned pending
invocation record containing only non-secret execution attribution:

- the actor name;
- the repository `HEAD` at invocation start;
- the invocation role/context;
- the phase ID, when applicable; and
- the validation identity or scope, when required for a repair or completion
  invocation.

It never stores the compiled context, objective, model output, provider final
message, parameter values, environment values, or secrets. The record is
written before `provider.Run` begins and is retained until the authority result
and any required phase attribution are durable. Pending invocation evidence
identifies which invocation caused `HEAD` movement; it is never phase acceptance
evidence.

After provider return, including a provider error, the runtime compares current
`HEAD` with the recorded start commit. If it moved, the movement is attributed
to the persisted actor and checked with the effective actor commit permission
rule. This applies to primary actors, validation repair actors, recovered
primary reruns, completion-validation repair actors, and all future actor calls
through the shared boundary.

On restart, pending invocation reconciliation occurs before actor replay,
deterministic validation, phase acceptance, dependency scheduling, or
completion. If `HEAD` moved during the interrupted invocation, reconciliation
performs the same attribution and permission check before any later action. If
the movement is unauthorized, reconciliation durably records a terminal safety
failure. If `HEAD` did not move, the pending record can be closed after that
fact is durably recorded. Reconciliation is execution recovery, not acceptance.

Recovery validates the saved base and branch lineage. It preserves useful
partial commits and worktree changes. If an active phase lacks durable
`actor_completed` evidence, the same actor is rerun under that rerun
invocation's own capabilities; if the actor returned successfully and only
deterministic acceptance was interrupted, recovery resumes acceptance without
replaying the actor. Completed markers are trusted only while their commits
remain valid ancestors of the current `HEAD`.

### Terminal safety state

`failure_kind: safety` in the active-phase record and a safety failure in the
standalone/final validation failure record are durable decisions for the
current run. Once present, they block actor execution and replay, validation
repair, deterministic acceptance, checkpointing, phase completion, dependency
release, and final completion. Moving `HEAD`, reverting a commit, cleaning the
workspace, or later passing validation does not clear the decision. Safety
failures never create another repair opportunity.

The explicit reset/abandon mechanism is the only way to discard terminal safety
state and begin a new run. Ordinary reruns may continue only from recoverable
deterministic validation failures; they cannot bypass or silently clear a
persisted safety failure.

### Shared acceptance invariants

These authority domains remain separate in both `v1alpha1` and `v1alpha2`,
which normalize to the same executable runtime model:

- deterministic validation owns advancement;
- actor/model output never authorizes acceptance;
- an actor-created commit never satisfies `dependsOn` by itself;
- successful repair actor execution is not acceptance; and
- successful completion requires the deterministic final validation gate.
