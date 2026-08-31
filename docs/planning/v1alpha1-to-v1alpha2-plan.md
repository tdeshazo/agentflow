The convergent solution is not “move all v1alpha1 fields into v1alpha2.” It is:

1. Preserve v1alpha2’s concise dependency-based core.
2. Add portable authority needed by real workflows.
3. Replace procedural v1alpha1 mechanisms with runtime-owned behavior.
4. Introduce typed handoffs before parallelism and composition.
5. Deprecate v1alpha1 only after self-hosting and representative workflows migrate without semantic loss.

This refines the direction already established in [ROADMAP.md](/home/travis/Workspace/agentflow/agentflow/ROADMAP.md:406).

## Target authoring model

A simple workflow should continue to require only:

- workspace write scope;
- agents;
- deterministic validations;
- dependency-linked phases;
- final validation.

Advanced workflows should progressively add:

- protected resources and lineage;
- typed inputs and conditions;
- deterministic tools and preconditions;
- human decisions;
- typed phase outputs and artifacts;
- rich completion policy;
- reusable workflow components.

Authors should never need to specify Git refs, active-phase records, checkpoint sequencing, recovery steps, or completion-marker ordering. Those must remain visible in the expanded plan, but runtime-owned.

## v1alpha1 feature disposition

| v1alpha1 capability | Decision for successor | Priority | Rationale |
|---|---|---:|---|
| Typed parameters, environment values, CLI overrides | Implement | P0 | Required for reusable and portable workflows. |
| Bounded expressions and conditions | Implement | P0 | Necessary for optional phases, human gates, and parameterized behavior. Keep the language deliberately small. |
| Named `paths` indirection | Do not port directly | — | Typed inputs and artifact references should cover this without a second indirection system. |
| Mutation allowlist | Already implemented | — | Preserve the current concise form. |
| Protected-resource integrity | Implement | P0 | A write allowlist alone cannot protect allowed files from forbidden semantic changes. |
| Lineage and initial cleanliness requirements | Implement as policy | P0 | These are observable safety requirements, unlike internal Git-state plumbing. |
| Checkpoint mechanics | Runtime-owned | — | Authors should select policy, not sequence staging, committing, and marker writes. |
| Named agent capabilities | Already implemented | — | Preserve explicit per-invocation capabilities. |
| `defaults.agent` inheritance | Do not port | — | Hidden inheritance complicates review and boolean overrides. Named agents and later reusable components are clearer. |
| Reusable deterministic tools and typed invocation arguments | Implement | P0/P1 | Shell-only validation is too narrow for effective orchestration. |
| Deterministic preconditions | Implement | P0 | Fail-fast repository, command, file, and initialization checks are essential. |
| Multi-step validation | Implement | P0 | Preserve the current `run` shorthand while permitting richer reusable gates. |
| Validation dependencies and durable evidence | Implement | P0 | Enables safe evidence reuse and later typed handoffs. |
| Bounded repair | Already implemented; enrich | P0 | Retain exactly bounded repair and deterministic revalidation. Add explicit hard-gate policy and complete failure context. |
| Phase `reasoning`/effort | Implement | P0 | Effective model allocation should be explicit per task. |
| Phase `kind` | Implement narrowly | P0 | At least distinguish mutable implementation from independent/read-only audit behavior. Avoid restoring obsolete `tool` and `human` phase kinds. |
| `requiresChange` | Implement | P0 | Implementation phases and audit phases have materially different acceptance rules. |
| Phase conditions | Implement | P0 | Conditions belong on dependency-graph nodes, not in a separate procedural flow. |
| Human gates | Implement | P0 | Human verification must be durable orchestration state, not prompt prose. |
| Criterion progress | Generalize, then implement | P1 | Preserve engine-owned exact-target advancement, but do not make Markdown checklists the core abstraction. |
| Markdown progress/bookkeeping | Adapter or reusable component | P2 | Useful compatibility feature, but too repository-format-specific for the core language. |
| Completion assertions | Implement | P0 | Final validation alone cannot express integrity, cleanliness, human, or exact-target completion requirements. |
| Completion marker sequencing | Runtime-owned | — | The runtime should guarantee that the terminal marker is written last. |
| Completion summaries | Runtime presentation | P2 | Keep semantic outputs typed; derive ordinary presentation automatically. |
| Explicit `flow` serialization | Do not port | — | `dependsOn` and stable ready-node scheduling replace it. |
| Conditional flow | Move conditions onto graph nodes | P0 | Keeps control and dependency semantics in one model. |
| Dynamic checklist loop | Do not port directly | — | Replace later with bounded typed collection expansion when demonstrated. |
| `phaseDefaults` lifecycle programs | Do not port | — | Procedural lifecycle plumbing is precisely what v1alpha2 should eliminate. |
| `recovery.activePhase` | Do not port | — | Safe resume and crash reconciliation must stay runtime-owned. |
| Custom state record names/layout | Do not port | — | They expose implementation details and inhibit backend evolution. |
| Reset/abandon semantics | Implement | P0 | Reset is observable operator policy even though record deletion mechanics are internal. |
| Temporary/log file configuration | Runtime-owned | — | Not portable workflow semantics. |

## Convergence plan

`Capability matrix → safety parity → self-hosting migration → typed handoffs → parallel DAG → reusable composition → stable API`

### Phase 1: Freeze and measure

- Put v1alpha1 into grammar-frozen maintenance mode.
- Create a machine-readable capability matrix classifying every v1alpha1 construct as:
  - direct successor capability;
  - runtime-owned equivalent;
  - generalized replacement;
  - compatibility-only.
- Establish five representative workflows:
  - simple implementation;
  - implementation plus independent audit;
  - AgentFlow self-hosting;
  - human-gated release;
  - criterion-driven multi-item workflow.
- Add `migrate --check` diagnostics before automatic rewriting.

Exit condition: every supported v1alpha1 field receives a deterministic migration classification.

### Phase 2: Close portable safety and control gaps

Implement the P0 successor features:

- typed parameters and bounded conditions;
- integrity, lineage, and initialization policy;
- deterministic preconditions;
- phase effort, intent, change requirement, and conditions;
- multi-step tools and validations;
- human gates;
- completion assertions;
- explicit reset/abandon policy.

Retain the current five-section concise form for workflows that do not need these extensions.

Exit condition: the self-hosting workflow can be represented without lifecycle, recovery, state-record, or explicit-flow plumbing and with at least equivalent authority.

### Phase 3: Migrate real workflows

Status: partially complete. Shipped examples and the human-gated
representative default to v1alpha2, and a v1alpha3 self-hosting representative
demonstrates the portable safety/control subset with typed handoffs. The
canonical workflow at `spec/agent-workflow-v1alpha1.yaml` remains v1alpha1.
The representative intentionally omits its criterion progress and Markdown
bookkeeping semantics; v1alpha4 supplies generalized typed work-item building
blocks, but they have not yet been applied to the canonical workflow and
verified as an equivalent migration. The exit condition below therefore
remains open.

- Migrate self-hosting first.
- Compare expanded normalized plans, not YAML fields.
- Then migrate shipped examples and one human-gated workflow.
- Update the authoring skill to default to the successor.
- Keep v1alpha1 versions during a compatibility period.

Exit condition: mutation, integrity, validation, repair, resume, human evidence, and completion authority are equal or stronger after migration.

### Phase 4: Add typed contracts

Status: completed in `agentflow.dev/v1alpha3`. Typed contracts are versioned
separately so the stable v1alpha2 grammar keeps its existing meaning.

This is the largest missing capability that v1alpha1 itself does not solve:

- typed phase inputs and outputs;
- named artifacts with identity and producer/consumer relationships;
- validation-produced evidence;
- read-only auditor inputs;
- conditions based on typed results rather than actor prose.

Exit condition: dependent phases never need to scrape final messages or infer state from incidental files.

### Phase 5: Generalize progress and iteration

Status: completed in `agentflow.dev/v1alpha4`. Typed work items, deterministic
exact-target advancement, bounded static collection expansion, and an optional
Markdown presentation adapter are versioned separately from v1alpha3.

Model work items or criteria as typed engine-owned state:

- stable criterion IDs;
- exact-target advancement;
- bounded collection processing;
- deterministic before/after invariants;
- Markdown checklist support as an adapter.

Exit condition: the existing v1alpha1 criterion workflow can migrate without putting Markdown mechanics into the core language.

### Phase 6: Introduce bounded parallel execution

Only after typed handoffs exist:

- run independent ready nodes concurrently;
- detect overlapping mutation scopes;
- define failure propagation and cancellation;
- persist scheduler state;
- support deterministic fan-out/fan-in and restart.

Exit condition: serial and parallel execution produce equivalent acceptance outcomes, and unsafe overlapping writes fail before invocation.

### Phase 7: Composition and tooling

- Reusable, version-pinned workflow components.
- Reusable validation and policy bundles.
- Provider/tool capability negotiation.
- Graph visualization.
- Semantic workflow diff.
- Editor schema integration and canonical formatting.

Exit condition: common patterns can be reused without hidden defaults or alternate authority paths.

## Compatibility policy

Existing v1alpha2 documents should retain their meaning. Optional additions can remain in v1alpha2 only while they preserve the current nouns and normalization model. If typed nodes, executor unions, or composition materially reshape the grammar, introduce the next alpha version instead of silently redefining v1alpha2.

v1alpha1 should be deprecated for new authoring only when:

- self-hosting uses the successor;
- every representative workflow has migrated or has a documented exception;
- migration diagnostics cover every supported construct;
- compatibility tests continue protecting existing v1alpha1 workflows;
- the authoring skill and primary examples prefer the successor.

## Recommended first implementation slice

The highest-value initial slice is:

1. Integrity and lineage policy.
2. Typed parameters and initialization-scoped preconditions.
3. Phase effort, condition, intent, and `requiresChange`.
4. Multi-step deterministic validation with declared dependencies.
5. Completion assertions.
6. Durable human gates.
7. `migrate --check`.

That slice makes v1alpha2 capable of replacing v1alpha1 for serious sequential workflows while preserving its ergonomic advantage. Typed artifacts, parallelism, progress generalization, and composition can then build on a stable authority foundation rather than forcing another procedural workflow model.
