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
