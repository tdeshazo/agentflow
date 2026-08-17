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
and actor permissions. The agent may implement or repair work, but it cannot
mark its own phase accepted, waive a protected boundary, extend a repair
budget, or write the workflow-complete transition.

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

After acceptance, checkpointing may preserve an agent-created commit or commit
allowed dirty files. The checkpoint asserts scope, stages only allowed files,
requires a clean worktree, and reasserts the policy. The completed-phase marker
is written at the accepted `HEAD`; completion bookkeeping is a separate
validated transition after all phases and required gates pass.

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
marker. These records survive process interruption without a separate state
database and are tied to repository commits.

Recovery validates the saved base and branch lineage. It preserves useful
partial commits and worktree changes. If an active phase lacks durable
`actor_completed` evidence, the actor is rerun; if the actor returned
successfully and only deterministic acceptance was interrupted, recovery resumes
acceptance without replaying the actor. Completed markers are trusted only
while their commits remain valid ancestors of the current `HEAD`. Safety
failures remain terminal, while only configured validation failures may consume
a bounded repair attempt.
