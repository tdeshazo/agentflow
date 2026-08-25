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

`may_commit` is invocation-scoped authority. It answers whether the specifically
named actor invocation may move repository `HEAD` by creating commits. It does
not belong to the surrounding phase merely because that phase has a primary
actor. The check applies independently to the primary phase actor, a
validation-repair actor, an actor rerun during recovery, a repair actor invoked
by `completion.validation`, and every future actor invocation through the
shared runtime.

An actor-created commit without that actor invocation's permission is a
repository-policy safety failure. It is terminal for the safety boundary: it
does not become a repair invitation, is not accepted because another actor has
`may_commit: true`, is not hidden by later successful validation, cannot satisfy
`dependsOn`, and cannot authorize completion.

Runtime-owned checkpoints are distinct from actor-created commits. An
invocation with `may_commit: false` still permits AgentFlow to checkpoint
validated, allowed dirty work. That runtime commit does not grant the actor
commit authority or change the invocation that was authorized.

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

## Durable execution and recovery

The interpreter stores workflow evidence in Git objects and namespaced refs,
including base/branch lineage, an active-phase record, completed phase markers,
human-gate evidence, integrity baselines, run identity, and the final completion
marker. Successful deterministic validations also have digest-only,
content-addressed evidence keyed by their definition, resolved inputs,
declared dependencies, relevant file contents, policy, run identity, and
acceptance context. A lookup still performs the safety boundary first, so this
evidence survives process interruption without becoming authority across a
changed tree, lineage, integrity boundary, scope, or repair policy. These
records survive process interruption without a separate state database and are
tied to repository commits or current workspace identity as appropriate.

Recovery validates the saved base and branch lineage. It preserves useful
partial commits and worktree changes. If an active phase lacks durable
`actor_completed` evidence, the same actor is rerun under that rerun
invocation's own capabilities; if the actor returned successfully and only
deterministic acceptance was interrupted, recovery resumes acceptance without
replaying the actor. Completed markers are trusted only while their commits
remain valid ancestors of the current `HEAD`. Safety failures remain terminal,
while only configured validation failures may consume a bounded repair attempt.
