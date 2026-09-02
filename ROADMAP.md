# AgentFlow Roadmap

AgentFlow is evolving into a portable YAML-based specification description language (SDL) for agent workflows, with a reference Go interpreter.

The authoring experience should feel familiar to users of systems such as GitHub Actions: declarative YAML, named execution units, explicit dependencies, reusable components, conditions, inputs/outputs, and machine-enforced runtime policy. AgentFlow is not intended to be syntax-compatible with GitHub Actions; its semantics are centered on AI-agent execution, deterministic validation, durable recovery, workspace authority, and human verification.

## Product direction

AgentFlow should make an agent workflow readable as a contract before it is executed.

The specification owns:

- actors and execution capabilities;
- control flow and dependencies;
- allowed effects and protected resources;
- deterministic validation and repair budgets;
- durable state, checkpoints, and recovery;
- human verification;
- evidence and completion conditions.

The Go interpreter is the reference implementation of those semantics. Unsupported executable constructs must fail closed rather than degrade into best-effort behavior.

Three principles guide the roadmap:

1. **Specification before implementation detail.** Portable semantics belong in YAML; process mechanics that are not semantic requirements belong in the runtime.
2. **Agents may act; validation authorizes advancement.** Model output is evidence, not workflow authority.
3. **Definition, execution, and assurance stay separable.** A reusable workflow definition, a concrete execution graph, and the observed execution trace are related but distinct artifacts.

## Minimum viable product: self-hosting

The minimum acceptable AgentFlow product is not merely a parser or an engine that can run a demonstration workflow. **AgentFlow plus its reference execution engine must be sufficient to drive real development of AgentFlow itself.**

Before work beyond the initial runtime foundation is treated as critical-path product development, the repository must contain a self-hosting workflow that can be invoked with the AgentFlow interpreter against this repository and can safely complete a bounded, real change to the specification or interpreter.

At minimum, that self-hosting workflow must exercise:

- one or more AI implementation/audit phases through the provider abstraction;
- runtime-enforced workspace mutation boundaries and protected files;
- deterministic repository-owned validation rather than agent self-approval;
- bounded repair after a failed validation;
- Git checkpointing and clean-worktree enforcement;
- interruption and resume without discarding accepted work;
- durable phase/completion evidence;
- completion assertions owned by the workflow engine; and
- a real change to `agentflow`, not a synthetic fixture repository.

A shell command may launch AgentFlow, but a bespoke shell orchestrator must not own the phase sequence, repair loop, progress decisions, checkpoint policy, or completion transition. Those responsibilities must be expressed in the AgentFlow YAML and executed by the Go interpreter.

Once AgentFlow can develop itself, later roadmap work should preferentially use
AgentFlow for AgentFlow development so specification/runtime gaps are discovered
through dogfooding.

## Current foundation

The repository already has the core of an executable `v1alpha1` direction:

- a field-level `agentflow.dev/v1alpha1` specification guide;
- an executable v1alpha2 concise authoring contract that preserves the
  v1alpha1 authority model;
- a complete reference YAML definition;
- a concrete workflow translated from an imperative orchestration script;
- an experimental Go interpreter;
- a provider-neutral execution interface with an initial Codex CLI adapter;
- Git-backed durable workflow state and lineage;
- mutation allowlists and integrity checks;
- Markdown progress tracking;
- deterministic validation with bounded repair;
- commit-aware checkpoints and resumable phases;
- durable human gates; and
- explicit completion assertions and workflow-complete state.

The current runtime intentionally implements a conservative subset of the broader descriptive specification. The roadmap first makes that subset mechanically precise and closes the runtime gap, then proves the system by using AgentFlow to develop AgentFlow itself.

---

## Delivered milestones

These milestones are retained as historical proof points rather than active
execution priorities. Their achieved status does not implicitly close every
foundation criterion in stage 1.

---

### Self-hosting development workflow

**Goal:** Prove the SDL and Go interpreter are useful enough to orchestrate AgentFlow's own development.

This is the MVP gate for the project. Later execution stages improve a system
that has already demonstrated that it can safely develop itself.

**MVP status: achieved.**

#### Scope

- Use the normal Go interpreter entry point to execute that workflow against the repository itself.
- Encode repository mutation policy, protected resources, validation, repair budget, checkpointing, recovery, and completion in YAML rather than a coordinating shell script.
- Use at least one real AI implementation phase and one independent audit/review phase with intentionally chosen model/reasoning assignments.
- Run deterministic Go/repository checks through AgentFlow-owned validation steps.
- Preserve cohesive agent-created commits when valid and checkpoint successful dirty work when needed.
- Prove interruption/restart behavior on a real self-development run.
- Make self-hosting workflow state namespaced so repeated development workflows cannot collide with ordinary Git refs or other AgentFlow runs.
- Produce useful `status` output while a self-hosted run is active, failed, recoverable, or complete.
- Use the self-hosting workflow to complete at least one real subsequent `agentflow` change and retain that run as documented evidence of dogfooding.

#### Exit criteria

- From a clean `agentflow` checkout, a developer can launch a self-development run with the AgentFlow CLI and a repository-owned YAML workflow.
- The Go interpreter, not a bespoke shell wrapper, owns phase advancement, validation/repair, checkpointing, recovery, and completion.
- A real AgentFlow source/specification change has been implemented and accepted through that workflow.
- Killing the interpreter after a durable checkpoint and rerunning it resumes from persisted AgentFlow state rather than replaying accepted work.
- A deliberately failing validation receives only its declared repair budget and cannot advance until deterministic checks pass.
- Protected files and out-of-scope mutations are rejected even when an agent attempts them.

---

### v1alpha2 concise authoring and conformance

**Status: achieved.** The concise AgentFlow evolution is strictly decoded,
validated as executable, normalized to the existing authority model, and
covered by checked-in conformance and runtime tests.

**Goal:** Define and prove the concise AgentFlow authoring surface before
extending its scheduler or runtime topology.

The v1alpha2 contract is an evolution of AgentFlow, not a vocabulary reset.
It preserves the existing `workspace`, `agents`, `validation`, `phases`, and
`completion` nouns, keeps `validation` singular, and normalizes concise
authoring into the existing executable authority concepts wherever possible.

#### Delivered scope

- Define the `agentflow.dev/v1alpha2` / `AgentWorkflow` top level and the
  concise `workspace.allowWrites`, named `agents`, named `validation` gates,
  dependency-aware `phases`, and `completion.validation` form.
- Specify that `workspace.allowWrites` is mutation authority normalized to the
  existing workspace mutation policy.
- Specify deterministic shell validation through `validation.<name>.run`.
- Specify `repair.once: <actor>` as exactly one repair attempt followed by
  deterministic revalidation; repair actor success never accepts work.
- Specify that `phase.dependsOn` requires deterministic acceptance of every
  referenced phase. Actor return, actor output, commits, and unvalidated
  workspace state do not satisfy dependencies.
- Specify fail-closed handling for unknown dependencies, self-dependencies,
  duplicate phase IDs, cycles, missing actors, and missing validations.
- Specify dependency-derived execution when explicit `flow` is omitted,
  beginning with a deterministic serial scheduler. Parallel execution is not
  part of the initial v1alpha2 contract.
- Specify `completion.validation` as a distinct deterministic final gate whose
  evidence is not inherited from prior successful phase validation.
- Define v1alpha2 named and inline agent capability parity for `runner`,
  `model`, `sandbox`, `approval`, `ephemeral`, `may_commit`, and
  `output_last_message`, with normalization into the shared `Agent` values.
- Keep v1alpha1-only inherited defaults such as `spec.defaults.agent` outside
  v1alpha2; capability parity does not claim full v1alpha1 schema parity.
- State explicitly that model/actor output never authorizes advancement or
  completion, and that v1alpha1 retains its existing behavior.
- Check in the representative `feature` example and prove strict decoding,
  executable validation, workspace authority, actor resolution, one repair
  attempt with deterministic rerun, dependency readiness, final validation,
  and durable completion evidence.

#### Exit criteria

- The v1alpha2 authoring contract is documented and linked from the reference
  indexes and repository front door.
- The contract includes a complete representative YAML form and an explicit
  normalization/authority mapping.
- The roadmap and contract state that v1alpha2 is executable with a
  deterministic serial scheduler; parallel execution remains later work.
- Review can evaluate v1alpha2 semantics and its normalized authority model
  without changing v1alpha1 behavior.

---

## Execution order

Stage numbers are execution dependencies, not merely themes. A later stage
must not claim completion before the required earlier-stage exit criteria are
met. Work may overlap when its prerequisites are already satisfied, while
correctness and security repairs to delivered behavior always preempt new
feature work.

The active queue is ordered by current product risk and dependency value. Stage
identifiers remain stable for specifications, evidence, and historical links;
priority is represented by table order rather than by renumbering stages.

| Priority | Stage | Active focus | Required outcome before the next priority |
| --- | --- | --- | --- |
| 1 | 3 | Run identity, trace, and supervised sessions | Establish stable run/node identities and a versioned explainable trace before adding attach/detach supervision and interactive session handoff. |
| 2 | 5 | Typed-contract closure | Audit the typed handoff, artifact, and evidence behavior already used by later delivered stages; close any conformance or self-hosting gaps and record durable completion evidence before extending the executor surface. Stage 5 remains active until it is explicitly marked complete. |
| 3 | 7 | Executor and tool extensibility | Stabilize capability-aware provider and tool contracts, prove them with a second provider implementation, and fail unsupported requirements before execution. |
| 4 | 8 | Reusable workflows and composition | Add typed, pinned, trust-aware local and remote composition only after executor capabilities and execution identity are stable. |
| 5 | 9 | Developer tooling and observability completion | Finish formatting, linting, graph and editor support, semantic comparison, documentation drift checks, and structured operational exports after the executable semantics settle. |
| 6 | 10 | `v1beta1` stabilization | Freeze only the semantics proven by the preceding priorities, then publish normative artifacts, migration paths, releases, and the intended license. |

The following dependency stages are complete and no longer compete for active
priority. Correctness or security regressions in them still preempt the queue.

| Stage | Delivered foundation |
| --- | --- |
| 1 | Executable schema, v1alpha1 runtime parity, and runtime-owned orchestration foundation closure. |
| 2 | Exclusive execution ownership, isolated actor workspaces, enforced effect scopes, explicit/redacted credentials, privileged-effect approval, and durable resource budgets. |
| 4 | v1alpha1 maintenance policy and canonical successor migration. |
| 5.5 | Deterministic invocation-context compilation; its deferred resource-budget enforcement was delivered by Stage 2. |
| 6 | Bounded parallel dependency scheduling and durable recovery. |

Security is a continuous release gate across every stage. Developer tooling is
incremental: a tool needed to satisfy an earlier exit criterion belongs to that
earlier stage rather than waiting for stage 9. The active order changes only
when a stage records durable completion evidence or a newly discovered
correctness or security issue requires preemption.

---

## Execution stage 1 — Foundation closure

The first execution stage is a closeout audit over three foundations that are
substantially implemented. It must distinguish delivered behavior from
remaining specification, diagnostic, compatibility, and evidence gaps. A
foundation is not complete merely because later milestones used part of it.

- [x] Foundation closure exit criteria are satisfied and linked to durable evidence.

---

### Stage 1A — Executable schema, validation, and diagnostics

**Goal:** Make an AgentFlow document mechanically understandable before any agent or tool is allowed to run.

#### Scope

- Define a machine-readable schema for the executable `v1alpha1` surface.
- Add `agentflow validate` for syntax, type, reference, and structural validation.
- Validate IDs and references across actors, tools, validations, phases, progress items, human gates, and completion blocks.
- Distinguish descriptive fields from executable fields explicitly.
- Produce source-aware diagnostics with YAML path and line/column information where practical.
- Reject unknown executable fields by default.
- Add positive and negative conformance fixtures for the specification and interpreter.
- Document the compatibility promise within an API version.

#### Exit criteria

- Invalid workflow references fail before workspace mutation.
- The reference workflow and shipped examples validate cleanly.
- A conformance test suite distinguishes supported, unsupported, and invalid constructs.
- The interpreter and documentation share one authoritative executable schema.

---

### Stage 1B — `v1alpha1` runtime parity

**Goal:** Execute the documented `v1alpha1` core without relying on shell-script-specific assumptions.

#### Scope

- Complete documented precondition, assertion, validation, checkpoint, progress, recovery, human-gate, and completion semantics.
- Expand the expression evaluator from the current narrow template subset into a deliberately bounded expression model.
- Implement dynamic bounded loops such as “next unchecked criterion.”
- Support conditional execution for phases, gates, and steps.
- Make parameter typing, defaults, environment-backed values, and override errors consistent.
- Make recovery semantics idempotent across partial commits, dirty working state, completed criteria, and interrupted validation.
- Verify state-lineage behavior under rebases, detached commits, branch changes, and invalidated phase markers.
- Remove interpreter behavior that exists only for the current reference workflow unless it is generalized into the specification.

#### Exit criteria

- Every executable construct documented as `v1alpha1` is either implemented or explicitly declared non-executable.
- The existing shell-orchestrated reference workflow can be expressed and completed through AgentFlow without semantic loss.
- Restarting the interpreter at any durable phase boundary does not require replaying accepted work.

---

### Stage 1C — Runtime-owned orchestration and concise SDL authoring

**Goal:** Make AgentFlow workflows materially more concise than equivalent imperative orchestrators by keeping workflow-specific policy in YAML while moving generic lifecycle mechanics into the runtime.

Early executable workflows proved that AgentFlow can preserve the semantics of
large shell orchestrators. They also exposed the next design constraint: a
declarative workflow should not have to restate how AgentFlow itself persists an
active phase, resumes it, checkpoints accepted work, advances progress, or
reuses deterministic evidence. This foundation stage therefore treats concise workflow
authoring as a compression and authority benchmark before adding more execution
topology.

#### Delivered implementation order

1. **Runtime-owned phase lifecycle and recovery.** Replace procedural lifecycle/recovery YAML with safe runtime defaults plus explicit policy overrides.
2. **Engine-owned progress and completion bookkeeping.** Let deterministic acceptance advance declared progress and completion metadata instead of asking an agent to edit its own acceptance state.
3. **Content-addressed deterministic validation evidence.** Reuse a successful deterministic check when its tool definition, inputs, and relevant workspace identity are unchanged.
4. **Concise authoring surface and expanded plan.** Remove repeated configuration from authored YAML while preserving a fully inspectable normalized execution contract.

#### Scope

##### Runtime-owned phase lifecycle and recovery

- Define a safe default lifecycle for mutable AI phases that covers clean-boundary checks, phase-start capture, durable actor-completion evidence, deterministic validation, checkpointing, completed-phase evidence, and active-state cleanup.
- Let workflows select concise lifecycle policy such as safe resume, accepted-phase checkpointing, and clean phase boundaries instead of spelling out each runtime operation.
- Derive normal interrupted-phase recovery from phase state and lifecycle policy rather than requiring a procedural `recovery` sequence in every workflow.
- Preserve explicit escape hatches for workflows that genuinely require non-default lifecycle behavior; defaults must never weaken authority or silently skip validation.
- Enforce workspace mutation policy, protected-resource integrity, branch lineage, and cleanliness continuously at relevant runtime mutation and acceptance boundaries so authors do not have to repeat scope/integrity assertions around every gate and checkpoint.
- Centralize equivalent lineage and safety declarations so the same invariant does not need parallel spelling under state, workspace, preconditions, validation, and completion.

##### Engine-owned progress and deterministic bookkeeping

- Reference progress criteria by stable IDs from phases instead of repeating the complete human-readable criterion text.
- Allow a criterion phase to declare that successful deterministic acceptance advances its targeted progress item; the phase actor should implement the criterion, not mark its own acceptance checkbox.
- Make the runtime enforce that only the targeted criterion advances and that the declared progress delta is satisfied.
- Add deterministic completion/bookkeeping operations for structured Markdown status, checklist, or index updates where the required transition is fully declarative.
- Remove bookkeeping-only model calls when the engine can perform the same constrained transition deterministically.
- Prefer structured Markdown-aware progress/bookkeeping semantics over shell normalization commands such as `sed` when protecting all non-bookkeeping content.

##### Deterministic validation evidence and reuse

- Make deterministic validation invocations produce durable evidence identified by the validation/tool definition, resolved non-secret inputs, and relevant workspace/content identity.
- Reuse successful evidence when a later workflow transition requires the same deterministic validation against an unchanged relevant workspace state.
- Invalidate cached evidence whenever relevant files, tool definitions, resolved inputs, policy, or other declared dependencies change.
- Preserve gate-specific repair budgets and failure classification; cached success must never convert a safety failure or stale validation into acceptance.
- Keep bounded failure logs available to repair actors without persisting unnecessary prompt, secret, or environment data.
- Exercise the canonical repository gate as the first content-addressed validation benchmark so post-audit and final transitions do not rerun identical work against the same tree.

##### Concise authoring model

- Add inherited agent/executor defaults so common runner, sandbox, approval, ephemeral, commit, and output settings are declared once.
- Add phase-kind and lifecycle defaults with clear overrides; common `criterion`, `implementation`, `audit`, and bookkeeping behavior should not require repeated boilerplate.
- Stabilize familiar declarative concepts such as `uses`, `with`, `if`, scoped `env`, and named references where they concretely reduce repeated YAML without blurring authority boundaries.
- Make repair configuration concise: a validation may name its bounded repair actor/policy and, by default, rerun the same deterministic validation after repair rather than repeating the validation step list.
- Keep temporary directories, log file naming, Git-ref record plumbing, and similar interpreter implementation details out of workflow YAML unless they are observable semantic requirements.
- Normalize parameter environment/default spelling onto the typed parameter model rather than embedding shell-style fallback expressions.
- Keep domain-specific phase prompts focused on the work itself; generic instructions such as marking criteria complete, preserving phase state, or obeying runtime-owned checkpoint semantics should not need prompt repetition.

##### Normalized executable plan

- Compile concise authoring syntax and runtime defaults into an explicit normalized workflow representation before execution.
- Add or extend `agentflow plan --expanded` so authors and reviewers can inspect the resolved lifecycle, recovery behavior, policy enforcement points, validation/repair behavior, progress transitions, and completion contract without reading interpreter source.
- Make validation operate on both the authoring document and the normalized executable representation so defaults cannot introduce unsupported or ambiguous behavior.
- Use the expanded representation as the basis for future semantic workflow comparison: authoring concision must not come at the cost of hidden behavior.

#### Exit criteria

- Representative workflows no longer require an explicit procedural active-phase recovery sequence for the normal safe-resume case.
- Workspace scope, protected integrity, lineage, and cleanliness policy are declared once and are enforced at every required runtime boundary.
- Criterion prompts no longer ask an agent to mark its own acceptance criterion complete; deterministic acceptance owns that transition.
- Completion bookkeeping that can be expressed as a constrained deterministic update no longer consumes an AI phase.
- Requiring the same deterministic gate twice against the same relevant workspace state reuses valid evidence; changing a declared dependency forces the gate to run again.
- Common agents, phase lifecycle behavior, and validation repair policy can be expressed through defaults/references rather than repeated blocks.
- `agentflow plan --expanded` exposes all runtime-generated lifecycle, recovery, validation, progress, checkpoint, and completion behavior before execution.
- A representative workflow can be read top-to-bottom primarily as domain policy and work intent rather than interpreter implementation detail.
- Actor authority, mutation authority, and deterministic validation authority remain structurally distinguishable in both concise and expanded forms.
- Any incompatible grammar change uses a new alpha API version with an explicit migration path rather than silently changing `v1alpha1` semantics.

---

## Execution stage 2 — Runtime security and execution ownership

**Goal:** Establish one enforceable execution owner and move security and resource control out of prompt promises and into runtime-enforced policy.

- [x] Stage 2 exit criteria are satisfied and linked to durable evidence.

This stage is a prerequisite for supervised sessions, successor migration, parallel scheduling, and executor extensibility. Correctness and security defects in an existing boundary preempt feature work in later stages.

### Scope

- Independent actor repositories that exclude authoritative Git history and runtime-private workflow definitions and controls.
- An exclusive active-run lease tied to stable process identity, with deterministic rejection of concurrent owners.
- Observable stale-owner recovery that cannot confuse PID reuse with a live workflow.
- Per-executor tool capabilities.
- Workspace and resource scopes beyond path allowlists.
- Network-access policy.
- Credential/secret scopes with redaction guarantees.
- Human approval requirements for privileged effects.
- Model-call, tool-call, token, time, repair, and optional monetary budgets.
- Cancellation and budget-exhaustion semantics.
- Policy inheritance and narrowing rules.
- Security-focused conformance fixtures, including prompt-injection-style attempts to exceed authority.

### Implementation status

**Complete (2026-09-01).** `run` and `reset` use an exclusive PID/start-token
lease with fail-closed stale-owner recovery. Actors execute in independent
depth-one repositories without authoritative Git history or runtime-private
paths. Successor policies enforce network and external capability scopes,
explicit credential injection and output redaction, durable human approval for
privileged effects before any affected actor or repair can run, narrowing-only
executor overrides, context cancellation,
and Git-backed model/tool/token/time/cost exhaustion. See the
[Stage 2 closure evidence](docs/evidence/stage-2-runtime-security.md).

### Exit criteria

- Actors cannot read authoritative repository history or runtime-private workflow controls through their execution workspace.
- Two processes cannot concurrently own or advance the same durable workflow run.
- A crashed owner can be distinguished from a live owner and recovered without weakening durable acceptance.
- An actor cannot gain a capability merely by asking for it in its prompt.
- Secrets are injected only into explicitly authorized execution contexts and are redacted from durable traces by default.
- Budget exhaustion produces a deterministic workflow state rather than uncontrolled retry behavior.

---

## Execution stage 3 — Run identity, supervised sessions, and trace foundation

**Goal:** Make a run explainable independently of the reusable workflow definition.

### Implementation status

**Identity and orchestration trace complete (2026-09-01).** Every initialized run
has an opaque durable run ID bound to its compatibility digests. Every phase
attempt has a distinct node-execution ID and monotonic per-node attempt number;
interrupted recovery retains both. The runtime writes a separate v1 JSONL trace
with monotonic sequence numbers under the repository's private Git directory,
and records attempt lifecycle, durable state transitions, validation and repair
outcomes, checkpoint commits, human decisions, phase acceptance, workflow
completion evidence, and bounded provider/tool metadata. Provider metadata
includes enforced request shape, duration, metering, and outcome without prompt,
reasoning, credentials, or output. Status exposes the schema, path, and current
identities. Existing digest-only run and active-phase records migrate without
replaying work. See the [Stage 3 identity and trace evidence](docs/evidence/stage-3-run-identity-trace.md).

Definition-aware `status --detail` now provides a bounded recent-event view in
both human-readable and stable JSON forms without treating the diagnostic trace
as authority. Supervised attach/detach sessions and `explain` remain open Stage
3 work.

This stage builds on the exclusive ownership boundary from stage 2. Stable run identity and lossless session supervision are required before migration broadens the preferred workflow surface or parallel execution increases runtime complexity.

### Scope

- [x] Define a versioned execution-trace schema distinct from the workflow SDL.
- [x] Assign stable run and node-execution identities.
- [x] Record state transitions, attempts, validations, repairs, checkpoints, human gates, and completion evidence.
- [x] Capture provider/tool metadata without requiring private model reasoning.
- [x] Add `agentflow status` detail suitable for both humans and automation.
- Add `agentflow explain` for “why is this node blocked/skipped/failed?”
- Add supervised run-session control with mutually exclusive active ownership:
  `agentflow attach` reconnects a terminal to a detached run, while an explicit
  detach operation hands a foreground run to runtime supervision without
  interrupting or replaying workflow work.
- Keep attachment distinct from read-only `logs --follow`: attachment must
  replay and stream session output, forward terminal signals and supported
  operator input, and reject stale identities or concurrent runners.

### Exit criteria

- A completed or failed run can be reconstructed at the orchestration level from its trace and Git evidence.
- Users can explain why a node did or did not execute without reading interpreter logs.
- A live workflow can move between foreground and detached operation in either
  direction without losing output, replaying an actor, or permitting two
  processes to own execution concurrently.
- Workflow-definition data is not conflated with run-specific trace data.
- Self-hosted development runs provide enough trace data to diagnose failed or repaired phases without inspecting ad hoc shell output.

---

## Execution stage 4 — `v1alpha1` maintenance and successor migration

**Implementation status (2026-08-31): migration complete.** The canonical
[`spec/agent-workflow.yaml`](spec/agent-workflow.yaml) self-hosting workflow
uses v1alpha4 typed work items and typed handoffs. Expanded-plan comparison and
runtime repair, resume, audit-evidence, and completion tests prove
equal-or-stronger authority. The unchanged v1alpha1 source remains executable
for compatibility; see the
[migration closure evidence](docs/evidence/canonical-self-hosting-migration.md).

**Goal:** Evolve the portable authority semantics proven in v1alpha1 into the
concise successor contract without carrying forward procedural runtime plumbing
or declaring v1alpha1 deprecated before a real migration path exists.

This stage begins after foundation closeout and the minimum security/run-identity gates are complete. The grammar freeze and capability matrix may proceed earlier, but canonical migration and deprecation claims require those prerequisites.

### Version policy

- Put `agentflow.dev/v1alpha1` into supported maintenance mode: freeze its
  authoring grammar, retain decoding/validation/execution compatibility, and
  continue correctness, safety, durability, and security fixes.
- Prefer the smallest sufficient successor API for new workflows: v1alpha2 for
  the concise core, v1alpha3 for typed handoffs, and v1alpha4 for typed work
  items. Do not claim that one successor mechanically replaces every
  compatibility-only v1alpha1 construct.
- Add new portable product semantics to the successor contract rather than
  extending v1alpha1 convenience syntax.
- If the capabilities needed for general self-hosting materially reshape the
  reviewed v1alpha2 grammar, advance to the next alpha version rather than
  silently broadening v1alpha2 into an incompatible contract.
- Treat formal deprecation as a product milestone, not a parser switch. A
  deprecated v1alpha1 document should remain readable, validatable, plannable,
  and executable for a compatibility period.

### Migrate authorities, not fields

Classify every v1alpha1 capability before extending the successor grammar.
Preserve portable workflow authority; move generic process mechanics into the
runtime; leave procedural compatibility syntax legacy-only.

#### Carry forward as portable successor semantics

- typed parameters, environment-backed inputs, CLI overrides, and deliberately
  bounded expressions/conditions;
- workspace mutation authority plus protected-resource integrity rules;
- explicit reset/abandon semantics and lineage requirements that are observable
  workflow policy;
- named actor capabilities and invocation-scoped commit authority;
- reusable deterministic tools and `uses` / typed `with` / `if` invocation;
- deterministic preconditions before mutable execution;
- named validation gates, declared validation dependencies, durable validation
  evidence, bounded repair, and deterministic revalidation;
- phase-level intent such as stable ID, actor, reasoning/effort, validation,
  condition, accepted dependencies, and whether a repository change is
  required;
- engine-owned criterion/progress advancement where progress is a real product
  requirement rather than model-authored bookkeeping;
- durable human verification with explicit acknowledgement, skip policy, and
  evidence; and
- rich completion policy: assertions, distinct final validation, protected
  boundaries, and durable terminal completion.

#### Make runtime-owned rather than successor authoring requirements

- Git-ref/state record names, temporary/log filenames, and other persistence
  plumbing;
- ordinary active-phase lifecycle sequencing, actor-completion persistence,
  checkpoint mechanics, accepted-phase markers, and active-state cleanup;
- normal safe-resume recovery sequencing and crash reconciliation;
- runtime checkpoint commit implementation details; and
- workflow-complete marker plumbing and routine completion presentation.

These mechanics must remain visible in `agentflow plan --expanded` and covered
by conformance tests even when they disappear from authored YAML.

#### Keep legacy-only unless a demonstrated use case requires a successor

- procedural `phaseDefaults` before/after lifecycle programs;
- procedural `spec.recovery` programs that restate the runtime state machine;
- arbitrary internal state-record naming;
- explicit `flow` whose only purpose is to serialize phases that can instead be
  represented through accepted-phase dependencies; and
- specialized loop/control constructs that have no demonstrated self-hosting or
  external workflow requirement.

Legacy constructs remain executable under v1alpha1 compatibility, but they do
not automatically earn one-for-one successor syntax.

### Migration work order

1. **Freeze the v1alpha1 grammar.** Document maintenance status and stop adding
   new convenience syntax while retaining shared-runtime correctness fixes.
2. **Maintain a successor capability matrix.** For every supported v1alpha1
   capability, record whether the migration is direct successor syntax,
   runtime-owned semantics, or legacy-only compatibility.
3. **Close real self-hosting gaps first.** Add successor semantics based on what
   AgentFlow's own development workflow and shipped representative workflows
   actually require, rather than mechanically cloning the v1alpha1 schema.
4. **Migrate the canonical reference/self-hosting workflow.** The preferred API
   must be capable of safely developing AgentFlow itself before v1alpha1 is
   formally deprecated.
5. **Migrate examples and the public authoring skill.** New authoring guidance
   should default to the successor API; v1alpha1 guidance should become
   compatibility/migration documentation.
6. **Add deterministic migration diagnostics.** Provide a command such as
   `agentflow migrate --check` that classifies v1alpha1 constructs as directly
   migratable, runtime-owned in the successor, requiring manual replacement, or
   unsupported legacy syntax. Automatic rewriting is optional; authoritative
   diagnostics are required.
7. **Formally deprecate v1alpha1 only after the gates below are met.**

### Deprecation gates

Migration readiness satisfied these gates on 2026-08-31. v1alpha1 remains in
grammar-frozen compatibility and maintenance mode. `agentflow validate` emits
a non-fatal authoring deprecation warning that directs new workflows to
v1alpha4 and existing workflows to `agentflow migrate --check`; removal, if
ever chosen, remains a separate release decision.

Formal v1alpha1 deprecation requires all of the following:

- the canonical workflow(s) under `spec/` and the repository's self-hosting
  development path use the successor API without loss of mutation, validation,
  repair, recovery, human-evidence, or completion authority;
- the public AgentFlow authoring skill defaults to the successor API;
- every shipped representative v1alpha1 workflow has either a semantically
  equivalent successor workflow or an explicit documented reason it remains
  compatibility-only;
- migration diagnostics identify every supported v1alpha1 construct that cannot
  be represented directly in the successor;
- shared-runtime compatibility tests continue to protect execution of existing
  v1alpha1 workflows; and
- release notes and reference documentation distinguish "deprecated for new
  authoring" from "unsupported" or "removed."

Deprecation should emit a non-fatal authoring/validation warning rather than
turning an otherwise valid v1alpha1 workflow into an error. Removal, if ever
appropriate, belongs to a later explicit compatibility break after a long
migration window and must not be a prerequisite for v1beta1.

### Exit criteria

- v1alpha1 is documented as supported maintenance/frozen authoring rather than
  the target for new language features.
- The capability matrix is complete enough to review migration by semantic
  authority instead of field-count parity.
- AgentFlow's canonical self-hosting/reference workflow runs on the successor
  API with equivalent fail-closed authority and durability.
- The authoring skill and primary examples use the successor API by default.
- `agentflow migrate --check` or equivalent deterministic tooling explains the
  migration status of an existing v1alpha1 workflow without executing it.
- Formal v1alpha1 deprecation occurs only after all deprecation gates are met.

---

## Execution stage 5 — Typed contracts, artifacts, and evidence

**Goal:** Make handoffs between execution units machine-checkable rather than prompt conventions.

Typed handoffs precede parallel scheduling so fan-out/fan-in and downstream readiness do not depend on scraping actor prose or sharing implicit mutable state.

### Scope

- Typed phase/node inputs and outputs.
- Named artifacts with producers, consumers, content identity, and optional persistence policy.
- Explicit evidence objects connecting completion claims to deterministic checks.
- Output references usable by later conditions, tools, and actors.
- Read-only auditor nodes that consume artifacts/evidence without mutation authority.
- Validation of required outputs before dependent nodes become ready.
- Clear distinction between workflow outputs and runtime logs/traces.

### Exit criteria

- A downstream node can depend on a typed output rather than scraping agent prose.
- Completion can cite structured evidence produced by validation.
- Missing or incompatible artifacts fail before dependent execution.
- The self-hosting workflow uses at least one typed contract/evidence path where it removes a prompt-level convention.

---

## Execution stage 5.5 — Invocation context compilation

**Implementation status (2026-08-30): complete, with budgeting deferred.** Every phase,
resumed-phase, and validation-repair actor now receives deterministic versioned
context compiled from normalized authority, verified direct contracts,
workspace state, and bounded durable failure evidence. The Codex adapter
validates and renders that provider-neutral context, while expanded plans show
the inclusion/exclusion recipe without resolving sensitive values. Context is
not persisted as acceptance state. Per-invocation token, byte, file-count,
monetary, and other resource-budget enforcement is explicitly deferred to
separate resource-control work and is not a Stage 5.5 exit criterion.

**Goal:** Compile the smallest sufficient, inspectable execution context for each actor from authoritative workflow and workspace state before bounded parallel scheduling multiplies context cost.

This stage follows typed contracts because the compiler consumes machine-checkable dependency outputs and evidence. It precedes parallelism because concurrent branches must not each reconstruct broad repository or run-history context independently.

### Scope

- Define a provider-neutral invocation-context representation distinct from durable workflow authority and provider-specific prompt text.
- Derive context from the workflow and phase objectives, accepted dependency identities, typed artifacts/outputs, relevant evidence, authorized workspace/resource scope, bounded validation failures, executor capabilities, and declared budgets.
- Make compilation deterministic for unchanged authoritative state except for explicitly declared dynamic retrieval inputs.
- Keep context a derived view: Git/workspace state, the workflow definition, typed artifacts/evidence, and durable run state remain authoritative.
- Include dependency artifacts by identity/reference and selected content instead of indiscriminate transcript or actor-output copying.
- Bound repair context to relevant deterministic failure evidence and authorized workspace state.
- Let provider adapters render compiled context without independently reconstructing workflow semantics.
- Extend `agentflow plan --expanded` or equivalent diagnostics to show the compiled context manifest and why each component is included, without exposing secrets.
- Establish explicit file/resource-scope metadata that stage 6 conflict analysis can reuse when deciding whether ready nodes may execute concurrently.
- Defer semantic indexing, embeddings, learned ranking, and autonomous retrieval until the deterministic compiler seam is proven.

### Explicitly deferred work

- Per-invocation context, token, byte, file-count, monetary, and other resource-budget enforcement remains part of separate resource-control work. It does not block Stage 5.5 completion.

### Exit criteria

- Every actor invocation receives a runtime-generated, inspectable context manifest.
- The same unchanged authoritative state compiles to equivalent context.
- Dependent nodes receive declared typed outputs/evidence without scraping prior actor prose or replaying full transcripts.
- Repair actors receive bounded relevant failure context rather than unbounded run history.
- Provider adapters consume normalized compiled context rather than re-deriving workflow authority.
- `agentflow plan` can explain why context elements are included or excluded without revealing secrets.
- Stage 6 can use the same normalized resource-scope metadata to distinguish safe disjoint concurrency from conflicts.
- A self-hosting workflow demonstrates materially smaller phase-specific context than passing broad repository/run history while preserving deterministic acceptance.

Conservative filesystem resource metadata is consumed by Stage 6 conflict
analysis. It does not constitute budget enforcement, which remains explicitly
deferred beyond Stage 5.5.

---

## Execution stage 6 — Parallel dependency graph and scheduler

**Implementation status (2026-08-30): complete.** Successor workflows retain
serial execution by default and may opt into a bounded scheduler with
`execution.maxParallel`. Ready read-only phases and phases with enforced,
provably disjoint `writes` scopes execute concurrently in isolated quarantine
workspaces. Provider failures cancel siblings; reconciliation, deterministic
validation, checkpointing, contract publication, and acceptance remain
authored-order transitions. Git-backed active-batch and per-node records make
parallel interruption recover through the ordinary safe-resume boundary.

Stage 6 consumes the compiler's resource metadata prerequisite. Token and other
invocation-budget enforcement remains explicitly deferred and is not a Stage 6
prerequisite or a Stage 5.5 exit gap.

**Goal:** Extend the reviewed v1alpha2 dependency contract beyond its implemented deterministic serial scheduler into bounded parallel execution while retaining deterministic advancement and recovery.

**Foundation status:** the dependency graph, accepted-dependency semantics, ready-node scheduler seam, and deterministic serial execution are implemented. This stage owns concurrency, fan-out/fan-in, conflict detection, and parallel recovery.

### Scope

- Model execution dependencies explicitly.
- Build a ready-node scheduler for DAG execution.
- Add bounded parallel execution of independent phases only after the initial
  v1alpha2 serial semantics are proven.
- Add fan-out/fan-in semantics.
- Define failure propagation, cancellation, skipped-node behavior, and downstream invalidation.
- Preserve deterministic validation and checkpoint rules when independent branches mutate different resources.
- Detect unsafe overlapping workspace mutation before concurrent execution.
- Persist scheduler state so interrupted parallel workflows resume safely.

### Exit criteria

- [x] Independent read-only or disjoint work executes concurrently in isolated
  actor workspaces.
- [x] Readiness and fan-in derive from accepted dependency markers rather than
  YAML order alone; authored order is only the deterministic selection and
  integration tie-breaker.
- [x] Restart validates and reconstructs active batches and per-node retained
  work from durable Git state.
- [x] Equal, nested, ambiguous, undeclared, and actor-commit mutation scopes
  are serialized; actual changes outside a declared phase scope fail closed.
- [x] The canonical v1alpha4 self-hosting workflow fans out two independent
  read-only audits and joins them before human verification/completion.

---

## Execution stage 7 — Executor and tool extensibility

**Goal:** Make the language portable across models, deterministic tools, humans, remote agents, and composite workflows.

New executors and tools must conform to the stage 2 capability boundary and the stage 5 typed contract model before they can participate in workflow execution.

### Scope

- Stabilize the public provider interface.
- Support multiple model providers without provider-specific fields leaking into portable workflow semantics.
- Define a generic executor capability model.
- Add a tool/plugin registration interface with typed configuration and declared mutation behavior.
- Support deterministic local tools, remote services, human executors, and nested AgentFlow workflows through the same execution model where appropriate.
- Add provider/tool capability negotiation so unsupported requirements fail before execution.
- Define versioning expectations for provider and tool plugins.

### Exit criteria

- At least two distinct execution-provider implementations pass the same provider contract suite.
- Custom deterministic tools can be registered without changing the core interpreter.
- A workflow can declare required capabilities without naming implementation-specific command-line flags.

---

## Execution stage 8 — Reusable workflows and composition

**Goal:** Let teams build libraries of trusted procedures instead of copying prompt-heavy YAML.

Composition follows typed contracts, stable execution identity, and executor capability enforcement so reuse does not introduce an alternate authority or trust path.

### Scope

- Reusable workflow fragments with typed inputs/outputs.
- Composite workflows as execution units.
- Versioned references and immutable pinning.
- Local and remote resolution rules.
- Trust policy for externally sourced workflow components.
- Reusable policies, validation bundles, and executor definitions.
- Recursion/cycle detection.
- Composition-aware diagnostics and trace provenance.

### Exit criteria

- Common phase/gate/recovery patterns can be packaged and reused without copy/paste.
- Remote reusable components can be pinned to immutable identities.
- The runtime can explain the fully resolved workflow before execution.
- Repeated self-hosting policy/gate definitions can be factored into reusable components without weakening reviewability.

---

## Execution stage 9 — Developer tooling and observability completion

**Goal:** Make the SDL pleasant to author and safe to review while completing
operational exports and aggregate observability that are not prerequisites for
safe execution.

Tooling required to complete an earlier stage ships with that stage—for example schema diagnostics in stage 1 and migration diagnostics in stage 4. This stage closes the remaining authoring, review, visualization, and drift-prevention experience after the executable semantics stabilize.

### Scope

- `agentflow fmt` for canonical formatting where safe.
- `agentflow lint` for semantic and policy guidance beyond schema validity.
- `agentflow plan` to display the resolved execution graph without running it; stage 1 establishes the expanded normalized-plan foundation this tooling builds on.
- Graph visualization for dependencies and gates.
- Shell/editor completion for CLI and workflow fields.
- JSON Schema or equivalent editor integration.
- High-quality examples covering sequential, conditional, recovery, DAG,
  human-gated, and reusable workflows.
- Semantic workflow comparison that distinguishes behavior changes from prompt-only text changes.
- Documentation generated from or verified against schema definitions where practical.
- Export structured run data for analysis and optional observability integrations.
- Track quality, latency, token/call counts, and cost where providers expose them.

### Exit criteria

- A workflow author can validate, inspect, and graph a workflow before execution.
- Reviewers can identify authority, dependencies, and completion conditions without tracing interpreter code.
- Documentation and schema drift are caught in CI.

---

## Execution stage 10 — `v1beta1` stabilization and conformance

**Goal:** Establish a version suitable for real external workflows and independent interpreter implementations.

Stabilization begins only after the preceding execution stages have either met their exit criteria or explicitly moved nonessential scope beyond `v1beta1`.

### Scope

- Resolve accumulated alpha design decisions informed by self-hosting experience.
- Publish a precise compatibility and deprecation policy.
- Freeze core SDL semantics for `v1beta1`.
- Publish schema artifacts and a normative conformance suite.
- Separate normative specification text from reference-interpreter guidance.
- Add migration tooling/documentation from supported alpha versions.
- Define portability requirements versus optional runtime extensions.
- Add release artifacts and installation instructions for the Go interpreter.
- Select and add the intended software license before public distribution.

### Exit criteria

- Another implementation can use the specification plus conformance fixtures without reverse-engineering the Go interpreter.
- Existing supported alpha workflows have a documented migration path.
- The reference interpreter passes the complete `v1beta1` conformance suite.
- AgentFlow's own development workflow runs on the stabilized semantics or has a documented migration proving those semantics are practical for a real repository.

---

## Later research directions

These are deliberately not on the critical path to a stable declarative runtime, but the SDL should avoid designs that make them impossible:

- dynamic manager/worker routing;
- search/tree-based execution strategies within bounded nodes;
- workflow optimization and candidate comparison;
- procedural workflow memory;
- formal verification backends;
- distributed schedulers and remote execution;
- policy-as-code integrations; and
- learned orchestration policies constrained by declarative authority.

---

## Roadmap governance

The roadmap should be updated when implementation, research, or self-hosting
experience materially changes these dependencies. The specification should not
claim support for a roadmap item until both its SDL semantics and
reference-interpreter behavior are covered by conformance tests and the
completion claim is linked to durable evidence.
