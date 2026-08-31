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
- `generalized-replacement`: a successor capability must model the
  authority without a mechanical field copy.
- `compatibility-only`: retain the v1alpha1 document until a successor
  replacement is explicitly specified.

Stable declared criteria, exact-target advancement, and compatible checklist
presentation now have a generalized successor in `agentflow.dev/v1alpha4`:
typed `spec.criteria` work items, phase-local `workItem`/`advanceWorkItem`, and
an optional `markdownChecklist` adapter. This is not a mechanical field rename.
In particular, v1alpha1 first-unchecked selection and `flow[].loop` can
rediscover work at runtime, while v1alpha4 `forEach` expands only a statically
declared, explicitly bounded collection. Those dynamic constructs remain
`compatibility-only`.

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
| [AgentFlow self-hosting](../../examples/representative/agentflow-self-hosting.agent-workflow.yaml) | v1alpha3 | Partial successor representative for portable safety/control and typed handoffs; canonical v1alpha1 criterion/progress and bookkeeping semantics are not included |
| [Human-gated release](../../examples/representative/human-gated-release.agent-workflow.yaml) | v1alpha2 | Phase 3 default successor; [v1alpha1 compatibility copy](../../examples/representative/human-gated-release-v1alpha1.agent-workflow.yaml) retained |
| [Criterion-driven multi-item workflow](../../examples/representative/criterion-driven-multi-item.agent-workflow.yaml) | v1alpha4 | Phase 5 typed work-item successor with Markdown retained only as a presentation adapter |

These are migration representatives. The self-hosting workflow demonstrates
Phase 3 portable safety/control and Phase 4 typed handoffs without lifecycle,
recovery, state-record, or explicit-flow plumbing; it does not establish an
equivalent migration of the canonical v1alpha1 workflow, which retains
criterion progress and Markdown bookkeeping semantics. The criterion-driven
workflow demonstrates the Phase 5 generalized replacement: typed engine-owned
work items with Markdown as an optional presentation adapter. Those semantics
have not yet been applied to the canonical self-hosting workflow. The shipped
art-portfolio workflow also defaults to v1alpha2, with its
[v1alpha1 compatibility copy](../../examples/art-portfolio-v1alpha1.agent-workflow.yaml)
retained. Follow the
[convergence plan](../planning/v1alpha1-to-v1alpha2-plan.md) and
[migration guide](../guides/migrating-v1alpha1-to-v1alpha2.md) for
authority-preserving source migration.
