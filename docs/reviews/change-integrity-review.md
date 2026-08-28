The integrity failure was correct; the recovery experience was not. AgentFlow prevented an unauthorized rules-data change from being accepted, but then gave an impossible recovery instruction and insufficient diagnostic detail.

### Root causes

1. Workflow boundary gap: the criterion required an edition-dependent fixed-damage limit, but `internal/ruleset/**` and `data/.../rules.yaml` were neither writable nor validation dependencies.
2. Runtime contract bug: `status` said “remediate then rerun,” while the engine intentionally rejects persisted terminal safety before re-evaluating the workspace.
3. Diagnostic gap: integrity state stores only one combined hash per rule, so AgentFlow could name the rule but not the changed file.
4. Recovery gap: changing the workflow boundary invalidated run identity, leaving reset plus manually authored reconciliation as the only practical path.

### Recommended upstream work

| Priority | Enhancement |
|---|---|
| P0 | Correct terminal-safety recovery guidance |
| P1 | Report changed integrity paths |
| P1 | Make reset cleanliness consistent |
| P1 | Add mutation-policy analysis to validation/plan |
| P1 | Give actors a runtime-owned boundary envelope |
| P2 | Add first-class audited salvage/reconciliation |
| P2 | Isolate actor edits in a temporary worktree |

#### P0 — Fix the contradictory recovery contract

[status.go](/home/travis/Workspace/agentflow/agentflow/internal/engine/status.go:151) and [status_projection.go](/home/travis/Workspace/agentflow/agentflow/internal/gitstate/status_projection.go:158) return `remediate-then-rerun`. The human footer goes further and says reset is unnecessary.

But [engine.go](/home/travis/Workspace/agentflow/agentflow/internal/engine/engine.go:468) rejects terminal safety before any normal recovery or validation, exactly as [runtime.md](/home/travis/Workspace/agentflow/agentflow/docs/reference/runtime.md:424) documents.

Change all text and JSON projections to:

```text
recovery: operator-action-required
next_action: reset-or-abandon
```

Tests currently assert the incorrect behavior and must be updated.

#### P1 — Persist path-level integrity manifests

[runtime_policy.go](/home/travis/Workspace/agentflow/agentflow/internal/engine/runtime_policy.go:245) stores only an aggregate hash keyed by rule ID. Store per-path digests as well, without storing file contents.

A violation should report bounded, structured information:

```text
integrity_rule: roadmap-and-rules-governance
changed:
  - data/mothership/v1.2/rules.yaml
added: []
removed: []
```

Preserve backward compatibility with old aggregate-only records. Expose these safe relative paths in both text and JSON status.

#### P1 — Make `reset` honor concise cleanliness

[Reset](/home/travis/Workspace/agentflow/agentflow/internal/engine/engine.go:721) checks only `spec.state.reset` cleanliness fields. It does not honor concise `spec.workspace.cleanliness`, even though the documentation says reset refuses when the workflow requires a clean implementation workspace.

For safe-resume workflows, reset should refuse when dirty state would cease to be a recoverable active phase—especially when `outside_recoverable_active_phase: required`.

Also add a read-only reset preflight:

```sh
agentflow reset --check
```

It should report dirty files, run-identity changes, and whether reset is currently admissible.

#### P1 — Improve validation and expanded plans

Add a resolved mutation matrix showing:

- allowed paths;
- exact/normalized integrity coverage;
- integrity exclusions;
- relevant validation dependencies;
- phases that can consume each path.

Warn when:

- an allowed path is fully covered by exact-hash integrity and is therefore effectively immutable;
- validation dependencies do not cover potentially changed allowed files;
- an integrity exclusion is undocumented or unnecessarily broad;
- a mutable path has no phase or engine-owned consumer.

Validation evidence should conservatively include actual changed allowed files even when authored dependency patterns omit them. Explicit dependencies must never make cached acceptance less safe.

#### P1 — Expose and clean up integrity fields

[model.go](/home/travis/Workspace/agentflow/agentflow/internal/workflow/model.go:197) supports `exclude`, but the public v1alpha1 reference does not document it. This incident could have retained `data/**` protection while excluding only `rules.yaml`.

Document and validate `exclude`, including relative-path and zero-match behavior.

Conversely, `allowed_semantic_changes` is accepted by the model but appears unused. Reject it until it has enforceable semantics, or implement and document it; silently accepting a safety-looking no-op is dangerous.

#### P1 — Give actors the actual execution boundary

The provider currently receives only the authored prompt in [runtime_phase.go](/home/travis/Workspace/agentflow/agentflow/internal/engine/runtime_phase.go:418).

Prepend a runtime-owned contract describing:

- writable path patterns;
- protected path patterns;
- engine-owned progress files;
- commit authority;
- the selected validation gate.

This does not replace enforcement, but it lets an agent report “the required rules file is protected” instead of spending 34 minutes producing work that must fail closed.

#### P2 — Add audited salvage, not silent terminal clearing

Do not weaken terminal safety or automatically rebaseline protected content.

Instead, introduce an explicit operator action that creates a new run generation while preserving provenance:

```sh
agentflow salvage --reason "workflow boundary omitted edition-owned rules data"
```

It should require:

- the protected violation to be reverted or explicitly authorized by a changed workflow;
- retained work to pass scope and hard validation;
- operator acknowledgement;
- a new run identity;
- an audit link to the abandoned terminal run.

A first-class reconciliation contract could then rerun hard final validation without requiring every workflow author to invent boolean parameters and conditional phases.

#### P2 — Quarantine actor changes

Longer term, execute actors in a temporary Git worktree or overlay. Import only policy-compliant changes into the primary checkout. Prohibited edits remain available for operator inspection without poisoning the active workspace.

This would preserve AgentFlow’s fail-closed behavior while making safety failures much cheaper to diagnose and recover.

No files were changed during this evaluation.
