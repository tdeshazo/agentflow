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

- Added Stage 6 bounded dependency scheduling for successor workflows.
  `execution.maxParallel` opts into concurrent isolated actor work, while
  enforced phase `writes` scopes, conservative conflict analysis, authored-order
  reconciliation/acceptance, sibling cancellation, Git-backed active batches,
  parallel status/reset handling, and restart recovery preserve the existing
  validation, checkpoint, contract, and safety boundaries. The v1alpha3
  self-hosting representative now fans out independent read-only audits.
- Added a deterministic, versioned invocation-context compiler for primary,
  resumed, and validation-repair actors. Provider requests now carry typed
  objectives, workspace/dependency state, declared artifact and evidence
  references, effective authority, validations, bounded repair failures, and
  an inspectable inclusion/exclusion manifest. The Codex adapter validates and
  canonically renders that context inside the quarantine workspace, and
  `plan --expanded` reports secret-free context recipes. Token and resource
  budget enforcement remains deferred.
- Advanced Phase 3 successor migration: the art-portfolio and human-gated
  release workflows now default to v1alpha2 with validated v1alpha1
  compatibility copies, and a v1alpha3 self-hosting representative covers the
  portable safety/control subset plus typed handoffs. The canonical workflow
  under `spec/` remains v1alpha1; its criterion progress and Markdown
  bookkeeping are intentionally absent from that representative, so canonical
  migration and the Phase 3 semantic-equivalence exit condition remain open.
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
