# Migrating a workflow to v1alpha2

`agentflow.dev/v1alpha2` is the concise authoring form for the normal safe
path: named actors, an explicit write allowlist, deterministic validation,
bounded repair, dependency-derived ordering, and a separate completion gate.
The runtime owns safe resume, phase checkpoints, durable phase evidence, and
completion evidence. Authors do not reproduce those lifecycle steps in phase
prompts or YAML.

This is a source migration, not an automatic rewrite. A migration is valid
only when the v1alpha2 contract can express every authority required by the
workflow. Never drop a v1alpha1 progress loop, Markdown bookkeeping
transition, custom state layout, or completion requirement merely to make a
document shorter. v1alpha2 directly supports portable parameters and
conditions, integrity and initialization policy, deterministic preconditions,
multi-step tools and validation, phase intent/change controls, human gates,
completion assertions, and reset policy.

v1alpha1 is grammar-frozen but supported in maintenance mode. Start with the
machine-readable [capability matrix](../reference/v1alpha1-capability-matrix.md):

```sh
agentflow migrate --check -f workflow-v1alpha1.yaml
```

The diagnostic is deterministic and read-only. It classifies every authored
field before any manual or future automatic rewrite; automatic rewriting is not
implemented.

## Procedure

1. Check migration classifications, validate the existing document, and save
   its expanded plan.

   ```sh
   agentflow migrate --check -f workflow-v1alpha1.yaml
   agentflow validate -f workflow-v1alpha1.yaml
   agentflow plan --expanded -f workflow-v1alpha1.yaml > before.yaml
   ```

2. Inventory every authority in the plan. The v1alpha2 core can migrate the
   write allowlist, named or inline actor capabilities, typed parameters,
   integrity/lineage/initialization policy, deterministic preconditions,
   reusable tools and multi-step validation, bounded repair, phase conditions,
   human gates, completion assertions, reset policy, dependencies, and final
   completion validation. Stop if the workflow requires a dynamic progress
   transition, Markdown bookkeeping transition, custom state layout, or
   another construct the matrix classifies as compatibility-only.

3. Create a new document with `apiVersion: agentflow.dev/v1alpha2`; do not
   change the version of the original in place. Translate only equivalent
   declarations:

   | v1alpha1 | v1alpha2 |
   | --- | --- |
   | `workspace.mutationPolicy.allowed` | `workspace.allowWrites` |
   | `agents.<name>` | `agents.<name>` |
   | shell tool plus named validation | `validation.<name>.run` |
   | reusable tool sequence | `tools` plus `validation.<name>.steps` |
   | `onFailure.strategy: repair-once` | `validation.<name>.repair.once` |
   | parameters and bounded conditions | `parameters`, `phases[].if`, `humanGates[].if` |
   | integrity / lineage / initialization | `workspace.integrity` / `workspace.initialization` |
   | deterministic preconditions | `preconditions` |
   | explicit dependency ordering | `phases[].dependsOn` |
   | `completion.*.finalValidation` | `completion.validation` |
   | human gate | `humanGates` |
   | completion assertions | `completion.assertions` |
   | reset policy | `reset.allow`, `reset.requireCleanWorkspace` |

4. Omit procedural `state`, `lifecycle`, `phaseDefaults`, phase `after`, and
   `recovery.activePhase` plumbing from the new v1alpha2 document. The
   runtime-derived safe-resume contract still checks mutation scope, declared
   integrity, lineage, validation, checkpointing, phase evidence, and final
   completion in its fixed authority order.

5. Validate and inspect the new expanded plan before executing it.

   ```sh
   agentflow validate -f workflow-v1alpha2.yaml
   agentflow plan --expanded -f workflow-v1alpha2.yaml > after.yaml
   ```

   Compare the `normalizedExecution` object as well as the summary fields.
   It contains every normalized execution-affecting workflow field, while
   generated authoring defaults have already been materialized at their
   owning authority locations. Confirm the write allowlist, actor
   `may_commit` permissions, validation/repair policy, dependency graph,
   safe lifecycle, checkpoint ordering, and completion validation are at
   least as strict as the original.

6. Add or retain focused checks for the migrated workflow's acceptance gate,
   recovery boundary, and completion condition. Keep the v1alpha1 document
   until the v1alpha2 definition has passed the same deterministic checks.

## Phase 3 migration set

The checked-in migration set compares expanded normalized plans in
`TestPhaseThreeMigrationsPreservePortableAuthority`; it does not treat
superficial YAML similarity as proof of equivalence.

| Default successor | Compatibility copy | Preserved authority |
| --- | --- | --- |
| [`examples/art-portfolio.agent-workflow.yaml`](../../examples/art-portfolio.agent-workflow.yaml) | [`examples/art-portfolio-v1alpha1.agent-workflow.yaml`](../../examples/art-portfolio-v1alpha1.agent-workflow.yaml) | Identical mutation scope, resolved agent capabilities, phase acceptance gates, bounded repair, and final validation; v1alpha2 adds explicit initialization, lineage, reset, and clean-completion policy. |
| [`examples/representative/human-gated-release.agent-workflow.yaml`](../../examples/representative/human-gated-release.agent-workflow.yaml) | [`examples/representative/human-gated-release-v1alpha1.agent-workflow.yaml`](../../examples/representative/human-gated-release-v1alpha1.agent-workflow.yaml) | The same release phase, deterministic gate, checklist, exact acknowledgement, and durable human evidence; v1alpha2 adds final completion validation and initialization policy. |
| [`examples/representative/agentflow-self-hosting.agent-workflow.yaml`](../../examples/representative/agentflow-self-hosting.agent-workflow.yaml) | [`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml) | Mutation/integrity policy, deterministic gate and bounded repair, safe resume, human evidence, and final completion are equal or stronger without lifecycle, recovery, state-record, or flow plumbing. |

The canonical self-hosting v1alpha1 source remains executable during
compatibility because its criterion progress and Markdown bookkeeping are
explicit Phase 5 generalized-replacement work. The v1alpha2 self-hosting
workflow intentionally does not claim to migrate those fields. The
criterion-driven representative remains v1alpha1 for the same reason.

## What does not migrate automatically

v1alpha2 deliberately does not reinterpret v1alpha1-only fields. A document
with `apiVersion: agentflow.dev/v1alpha1` retains v1alpha1 semantics, and a
v1alpha2 document rejects v1alpha1-only fields rather than silently accepting
or weakening them. The authoritative field sets are the
[v1alpha1 field guide](../reference/agentflow-v1alpha1.md) and the
[v1alpha2 authoring contract](../reference/agentflow-v1alpha2.md).
