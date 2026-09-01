# Migrating a v1alpha1 workflow to a successor API

`agentflow.dev/v1alpha2` is the concise successor for the normal safe path,
`v1alpha3` adds typed artifacts and evidence, and `v1alpha4` adds exact durable
work items and an optional Markdown presentation adapter. All three use named
actors, an explicit write allowlist, deterministic validation, bounded repair,
dependency-derived ordering, and a separate completion gate. The runtime owns
safe resume, phase checkpoints, durable phase evidence, and completion
evidence. Authors do not reproduce those lifecycle steps in prompts or YAML.

This is a source migration, not an automatic rewrite. A migration is valid
only when a successor contract can express every authority required by the
workflow. Never drop a dynamic v1alpha1 progress loop, custom state layout, or
completion requirement merely to make a document shorter. v1alpha2 directly supports portable parameters and
conditions, integrity and initialization policy, deterministic preconditions,
multi-step tools and validation, phase intent/change controls, human gates,
completion assertions, and reset policy. v1alpha4 can replace stable exact
criteria and checklist presentation, but not runtime discovery or arbitrary
bookkeeping transitions.

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

2. Inventory every authority in the plan. The successor core can migrate the
   write allowlist, named or inline actor capabilities, typed parameters,
   integrity/lineage/initialization policy, deterministic preconditions,
   reusable tools and multi-step validation, bounded repair, phase conditions,
   human gates, completion assertions, reset policy, dependencies, and final
   completion validation. Stop if the workflow requires a dynamic progress
   transition, Markdown bookkeeping transition, custom state layout, or
   another construct the matrix classifies as compatibility-only. Stable exact
   criteria may use v1alpha4 typed work items; keep dynamic discovery and
   specialized bookkeeping in the compatibility source.

3. Create a new document with the smallest sufficient successor API; do not
   change the version of the original in place. Use v1alpha2 for the concise
   core, v1alpha3 for typed handoffs, or v1alpha4 for typed work items.
   Translate only equivalent declarations:

   | v1alpha1 | successor |
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
   | stable exact criteria | v1alpha4 `criteria.items` plus phase `workItem` / `advanceWorkItem` |
   | compatible checklist presentation | v1alpha4 `criteria.markdownChecklist` |

4. Omit procedural `state`, `lifecycle`, `phaseDefaults`, phase `after`, and
   `recovery.activePhase` plumbing from the successor document. The
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
| [`examples/representative/human-gated-release.agent-workflow.yaml`](../../examples/representative/human-gated-release.agent-workflow.yaml) | [`examples/representative/human-gated-release-v1alpha1.agent-workflow.yaml`](../../examples/representative/human-gated-release-v1alpha1.agent-workflow.yaml) | The same release phase, deterministic gate, checklist, exact acknowledgement, and durable human evidence; v1alpha2 makes the evidence record runtime-owned and adds final completion validation and initialization policy. |
| [`spec/agent-workflow.yaml`](../../spec/agent-workflow.yaml) | [`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml) | The canonical v1alpha4 workflow preserves or strengthens mutation, integrity, validation, one-shot repair, safe resume, human evidence, independent audit, and completion authority. Stable criteria use exact durable work items; Markdown is a runtime-owned presentation adapter. |

Canonical self-hosting migration is complete. The
[closure evidence](../evidence/canonical-self-hosting-migration.md) records the
expanded-plan comparison and runtime proof. The v1alpha1 source remains
executable as a compatibility definition, including its specialized status
and index presentation transitions.

## What does not migrate automatically

Successor APIs deliberately do not reinterpret v1alpha1-only fields. A document
with `apiVersion: agentflow.dev/v1alpha1` retains v1alpha1 semantics, and a
successor document rejects v1alpha1-only fields rather than silently accepting
or weakening them. The authoritative field sets are the
[v1alpha1 field guide](../reference/agentflow-v1alpha1.md) and the
[versioned successor references](../reference/README.md).
