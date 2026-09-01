# Canonical self-hosting migration closure

Status: complete on 2026-08-31.

AgentFlow's canonical self-hosting workflow is
[`spec/agent-workflow.yaml`](../../spec/agent-workflow.yaml), authored with
`agentflow.dev/v1alpha4` work-item semantics. The previous
[`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml)
definition remains unchanged, valid, plannable, and executable as the
compatibility source.

## Authority comparison

The comparison uses expanded normalized plans rather than YAML shape.
`TestPhaseThreeMigrationsPreservePortableAuthority` enforces the following
relationships:

| Authority | Closure result |
| --- | --- |
| Mutation | Actor-writable paths are identical to the v1alpha1 source. The only additional allowlisted path is `spec/agent-workflow-progress.md`, which the runtime owns as a presentation adapter. |
| Integrity | Exact-hash rules protect repository instructions, the canonical and compatibility workflows, the public authoring skill, the canonical quality gate and CI delegation, the roadmap, and planning guidance. |
| Validation | Baseline, implementation, verification, two independent audits, and final completion all use deterministic validation. Audit and final gates are hard failures. |
| Repair | Implementation and verification retain exactly one repair attempt by the implementer. Runtime coverage deliberately fails the implementation gate once and proves the single repair can complete the workflow. |
| Resume | The normalized successor requires a clean named branch, base ancestry, same-branch resume, and runtime-owned safe resume. Runtime coverage interrupts after durable actor completion and proves recovery does not replay that actor. |
| Human evidence | The v1alpha1 checklist and exact-text acknowledgement are preserved. v1alpha4 makes evidence identity and conditional skip durability runtime-owned. |
| Completion | Completion requires both independent audit evidence records, the final deterministic validation, workspace integrity, and a clean implementation workspace. A completed restart performs no provider replay. |

The v1alpha1 status-line and planning-index transitions were presentation
bookkeeping rather than portable acceptance authority. The successor replaces
criterion acceptance with two exact, durable v1alpha4 work items and mirrors
them through [`spec/agent-workflow-progress.md`](../../spec/agent-workflow-progress.md).
The adapter cannot advance work and actors cannot edit it. The specialized
status and index transitions remain available only in the v1alpha1
compatibility definition.

## Executable proof

The checked-in tests cover both the normalized contract and the actual runtime
boundary:

- `internal/workflow/migration_test.go` compares the v1alpha1 and v1alpha4
  expanded plans and rejects weakened mutation, integrity, validation, repair,
  resume, human-evidence, work-item, audit, or completion authority.
- `internal/engine/reference_workflow_test.go` runs the canonical file in an
  isolated Git repository and proves typed work-item advancement, runtime-owned
  Markdown mirroring, typed audit evidence, final cleanliness, bounded repair,
  interruption recovery, and idempotent restart.
- `internal/workflow/conformance_test.go` and `validate_test.go` require both
  canonical and compatibility definitions to remain executable.

Reproduce the focused proof from the repository root:

```sh
go run . migrate --check -f spec/agent-workflow-v1alpha1.yaml
go run . validate -f spec/agent-workflow.yaml
go run . plan --expanded -f spec/agent-workflow.yaml
go test ./internal/workflow -run 'TestPhaseThreeMigrationsPreservePortableAuthority|TestConformanceShippedDefinitions|TestReferenceDocumentsAreSpecValid' -count=1
go test ./internal/engine -run 'TestCanonicalSelfHostingWorkflow|TestReferenceV1Alpha1' -count=1
```

The repository-wide closure gate remains `scripts/check.sh`.

## Compatibility and deprecation boundary

The migration gates are satisfied: canonical self-hosting uses a successor,
representatives have successors or documented compatibility dispositions, the
migration matrix covers the supported v1alpha1 grammar, compatibility tests
remain active, and public authoring guidance prefers successor APIs.

This closes migration; it does not remove v1alpha1. v1alpha1 remains in
grammar-frozen maintenance mode and its runtime compatibility remains part of
the repository gate. Any future user-facing deprecation warning or removal is
a separate release decision and must remain non-fatal during the compatibility
period.
