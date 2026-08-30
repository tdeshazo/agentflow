# v1alpha1 capability matrix

`agentflow.dev/v1alpha1` is in supported, grammar-frozen maintenance mode.
Its decoder, validator, planner, and runtime compatibility remain supported;
new authoring syntax and portable product semantics belong in a successor API.

The machine-readable [capability matrix](../../internal/workflow/v1alpha1_migration_matrix.json)
is the authoritative Phase 1 inventory. Every v1alpha1 field pattern has one
of four deterministic dispositions:

- `direct-successor-capability`: v1alpha2 already expresses the authority.
- `runtime-owned-equivalent`: successor runtime behavior preserves it without
  procedural YAML.
- `generalized-replacement`: a future successor capability must model the
  authority without a mechanical field copy.
- `compatibility-only`: retain the v1alpha1 document until a successor
  replacement is explicitly specified.

Use the checked-in matrix through the CLI before attempting a source migration:

```sh
agentflow migrate --check -f workflow-v1alpha1.yaml
```

The command validates the source, reports every authored field with its source
location and classification, and never opens a repository, invokes a provider,
or rewrites YAML. Automatic rewriting is intentionally not implemented.

The five representative workflows establish the Phase 1 baseline:

| Workflow | API | Migration status |
| --- | --- | --- |
| [Simple implementation](../../examples/representative/simple-implementation.agent-workflow.yaml) | v1alpha2 | Successor core |
| [Implementation plus independent audit](../../examples/representative/implementation-independent-audit.agent-workflow.yaml) | v1alpha2 | Successor core |
| [AgentFlow self-hosting](../../examples/representative/agentflow-self-hosting.agent-workflow.yaml) | v1alpha2 | Phase 2 portable safety/control |
| [Human-gated release](../../examples/representative/human-gated-release.agent-workflow.yaml) | v1alpha2 | Phase 2 durable human gate |
| [Criterion-driven multi-item workflow](../../examples/representative/criterion-driven-multi-item.agent-workflow.yaml) | v1alpha1 | Compatibility until typed engine-owned work items land |

These are migration representatives. The self-hosting workflow demonstrates
Phase 2 portable safety/control without lifecycle, recovery, state-record, or
explicit-flow plumbing. Follow the
[convergence plan](../planning/v1alpha1-to-v1alpha2-plan.md) and
[migration guide](../guides/migrating-v1alpha1-to-v1alpha2.md) for
authority-preserving source migration.
