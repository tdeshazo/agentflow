# Changelog

## Unreleased

### Fixed

- Made the expanded execution plan explicitly report invocation-scoped
  `may_commit` enforcement after provider errors and distinguish runtime-owned
  checkpoint commits from actor-created commits.
- Gave v1alpha1 `Completion.FinalValidation` the same completion-scoped repair
  durability as v1alpha2, including crash-safe budget consumption and
  marker-before-cleanup ordering. Generic standalone `flow.validate` remains
  outside this transition-scoped contract.

### Added

- Added stable opaque run and node-execution identities with recovery-preserved
  attempts, provider attribution metadata, status projection, compatible legacy
  migration, and a separate versioned append-only execution trace under the
  repository's private Git directory.
- Completed orchestration trace coverage for attempt lifecycle, durable node
  transitions, validation reuse/failure/repair, repair exhaustion, checkpoint
  commits, human decisions, phase acceptance, crash reconciliation, and
  workflow completion evidence without persisting diagnostic output.
- Added allowlisted provider request/response and tool metadata to execution
  traces, including enforced policy shape, opaque static-model correlation,
  metering, duration, classified outcomes, tool declarations, skips, and shell
  exit codes while excluding prompts, reasoning, credentials, and output.
- Completed Stage 2 runtime security and resource control with fail-closed
  execution-policy inheritance, provider-enforced network/capability scopes,
  explicit credential injection and output redaction, human approval for
  privileged effects, durable model/tool/token/time/cost budgets, cancellation,
  and deterministic exhaustion state.
- Enforced a process-identity-backed exclusive workflow owner lease for runtime
  execution and reset, with safe stale-owner recovery.
- Added a non-fatal `agentflow validate` warning when a workflow uses the
  grammar-frozen `agentflow.dev/v1alpha1` API. Existing v1alpha1 workflows
  remain valid and executable; the warning directs new authoring to v1alpha4
  and existing workflows to `agentflow migrate --check`.
- Added Stage 6 bounded dependency scheduling for successor workflows.
  `execution.maxParallel` opts into concurrent isolated actor work, while
  enforced phase `writes` scopes, conservative conflict analysis, authored-order
  reconciliation/acceptance, sibling cancellation, Git-backed active batches,
  parallel status/reset handling, and restart recovery preserve the existing
  validation, checkpoint, contract, and safety boundaries. The canonical
  v1alpha4 self-hosting workflow fans out independent read-only audits.
- Added a deterministic, versioned invocation-context compiler for primary,
  resumed, and validation-repair actors. Provider requests now carry typed
  objectives, workspace/dependency state, declared artifact and evidence
  references, effective authority, validations, bounded repair failures, and
  an inspectable inclusion/exclusion manifest. The Codex adapter validates and
  canonically renders that context inside the quarantine workspace, and
  `plan --expanded` reports secret-free context recipes. Token and resource
  budget enforcement is explicitly deferred to separate resource-control work
  and does not block Stage 5.5 completion.
- Completed Phase 3 canonical migration. `spec/agent-workflow.yaml` is the
  v1alpha4 self-hosting workflow with typed handoffs, exact durable work items,
  runtime-owned checklist presentation, independent parallel audits, bounded
  repair, and fail-closed completion. Expanded-plan and runtime tests prove
  equal-or-stronger mutation, integrity, validation, repair, resume,
  human-evidence, and completion authority. The unchanged
  `spec/agent-workflow-v1alpha1.yaml` remains executable for compatibility.
- Added Phase 2 portable v1alpha2 authority: typed parameters and bounded
  conditions; integrity, initialization, and lineage policy; deterministic
  preconditions; reusable multi-step tools and hard validations; phase intent,
  kind, and change requirements; durable human gates; completion assertions;
  and explicit reset permission. The self-hosting and human-gated release
  representatives now use this surface without procedural lifecycle,
  recovery, state-record, or flow declarations.
- Put `agentflow.dev/v1alpha1` into grammar-frozen maintenance mode, with a
  machine-readable capability matrix, five representative migration workflows,
  and read-only `agentflow migrate --check` diagnostics that classify every
  supported field deterministically before any rewrite.
- Deterministic adversarial conformance coverage for pending actor attribution,
  bounded repair recovery, completion repair recovery, invocation-scoped
  commit authority, terminal safety, dependency/acceptance separation, and
  redacted workflow-scoped pending state across v1alpha1 and v1alpha2.
- Experimental Go interpreter for the executable `v1alpha1` core.
- Concise `agentflow.dev/v1alpha2` authoring with strict decoding, named actor
  resolution, inline one-off actors, workspace allowlists, dependency-derived
  serial scheduling, bounded repair, deterministic final validation, and
  durable completion evidence.
- Expanded v1alpha2 named and inline agents with strict decoding and shared
  `Agent` normalization for `sandbox`, `approval`, `ephemeral`, `may_commit`,
  and `output_last_message`; v1alpha1-only defaults such as
  `spec.defaults.agent` remain outside the v1alpha2 contract.
- Checked-in v1alpha2 conformance and example workflows, with v1alpha1
  compatibility regressions and expanded-plan coverage.
- Git-object/ref-backed durable workflow state and resumable phase markers.
- Public provider-neutral execution interface with an initial Codex CLI adapter.
- Runtime documentation and tests for Git state, glob policy, templates, and Codex invocation.

All notable changes to this repository should be documented here.

## 0.1.0 - Initial package

- Added the `agentflow.dev/v1alpha1` AgentWorkflow reference definition.
- Added the AgentFlow v1alpha1 field reference.
- Added the `agentflow-spec` skill.
- Added the Priority 5 combat workflow example.
