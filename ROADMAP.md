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
- a real change to `agentflow-spec`, not a synthetic fixture repository.

A shell command may launch AgentFlow, but a bespoke shell orchestrator must not own the phase sequence, repair loop, progress decisions, checkpoint policy, or completion transition. Those responsibilities must be expressed in the AgentFlow YAML and executed by the Go interpreter.

The self-hosting workflow should remain in the repository as a continuously exercised example and regression target. Once AgentFlow can develop itself, later roadmap work should preferentially use AgentFlow for AgentFlow development so specification/runtime gaps are discovered through dogfooding.

## Current foundation

The repository already has the core of an executable `v1alpha1` direction:

- a field-level `agentflow.dev/v1alpha1` specification guide;
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

## Priority 1 — Executable schema, validation, and diagnostics

**Goal:** Make an AgentFlow document mechanically understandable before any agent or tool is allowed to run.

### Scope

- Define a machine-readable schema for the executable `v1alpha1` surface.
- Add `agentflow validate` for syntax, type, reference, and structural validation.
- Validate IDs and references across actors, tools, validations, phases, progress items, human gates, and completion blocks.
- Distinguish descriptive fields from executable fields explicitly.
- Produce source-aware diagnostics with YAML path and line/column information where practical.
- Reject unknown executable fields by default.
- Add positive and negative conformance fixtures for the specification and interpreter.
- Document the compatibility promise within an API version.

### Exit criteria

- Invalid workflow references fail before workspace mutation.
- The reference workflow and shipped examples validate cleanly.
- A conformance test suite distinguishes supported, unsupported, and invalid constructs.
- The interpreter and documentation share one authoritative executable schema.

---

## Priority 2 — `v1alpha1` runtime parity

**Goal:** Execute the documented `v1alpha1` core without relying on shell-script-specific assumptions.

### Scope

- Complete documented precondition, assertion, validation, checkpoint, progress, recovery, human-gate, and completion semantics.
- Expand the expression evaluator from the current narrow template subset into a deliberately bounded expression model.
- Implement dynamic bounded loops such as “next unchecked criterion.”
- Support conditional execution for phases, gates, and steps.
- Make parameter typing, defaults, environment-backed values, and override errors consistent.
- Make recovery semantics idempotent across partial commits, dirty working state, completed criteria, and interrupted validation.
- Verify state-lineage behavior under rebases, detached commits, branch changes, and invalidated phase markers.
- Remove interpreter behavior that exists only for the current Priority 5 example unless it is generalized into the specification.

### Exit criteria

- Every executable construct documented as `v1alpha1` is either implemented or explicitly declared non-executable.
- The existing shell-orchestrated reference workflow can be expressed and completed through AgentFlow without semantic loss.
- Restarting the interpreter at any durable phase boundary does not require replaying accepted work.

---

## Priority 3 — Self-hosting development workflow

**Goal:** Prove the SDL and Go interpreter are useful enough to orchestrate AgentFlow's own development.

This is the MVP gate for the project. Priorities after this point are improvements to a system that has already demonstrated that it can safely develop itself.

**MVP status: achieved.** The retained proof is documented in [docs/evidence/self-hosting-mvp.md](docs/evidence/self-hosting-mvp.md).

### Scope

- Add a repository-owned AgentFlow workflow for development of `agentflow-spec`, for example `examples/develop-agentflow.agent-workflow.yaml`.
- Use the normal Go interpreter entry point to execute that workflow against the repository itself.
- Encode repository mutation policy, protected resources, validation, repair budget, checkpointing, recovery, and completion in YAML rather than a coordinating shell script.
- Use at least one real AI implementation phase and one independent audit/review phase with intentionally chosen model/reasoning assignments.
- Run deterministic Go/repository checks through AgentFlow-owned validation steps.
- Preserve cohesive agent-created commits when valid and checkpoint successful dirty work when needed.
- Prove interruption/restart behavior on a real self-development run.
- Make self-hosting workflow state namespaced so repeated development workflows cannot collide with ordinary Git refs or other AgentFlow runs.
- Produce useful `status` output while a self-hosted run is active, failed, recoverable, or complete.
- Use the self-hosting workflow to complete at least one real subsequent `agentflow-spec` change and retain that run as documented evidence of dogfooding.
- Add CI coverage that validates the self-hosting workflow definition and tests its deterministic/runtime semantics without requiring live model calls.

### Exit criteria

- From a clean `agentflow-spec` checkout, a developer can launch a self-development run with the AgentFlow CLI and a repository-owned YAML workflow.
- The Go interpreter, not a bespoke shell wrapper, owns phase advancement, validation/repair, checkpointing, recovery, and completion.
- A real AgentFlow source/specification change has been implemented and accepted through that workflow.
- Killing the interpreter after a durable checkpoint and rerunning it resumes from persisted AgentFlow state rather than replaying accepted work.
- A deliberately failing validation receives only its declared repair budget and cannot advance until deterministic checks pass.
- Protected files and out-of-scope mutations are rejected even when an agent attempts them.
- The self-hosting workflow remains a maintained example/conformance fixture for subsequent roadmap work.

---

## Priority 4 — Runtime-owned orchestration and concise SDL authoring

**Goal:** Make AgentFlow workflows materially more concise than equivalent imperative orchestrators by keeping workflow-specific policy in YAML while moving generic lifecycle mechanics into the runtime.

The first executable examples proved that AgentFlow can preserve the semantics of large shell orchestrators. They also exposed the next design constraint: a declarative workflow should not have to restate how AgentFlow itself persists an active phase, resumes it, checkpoints accepted work, advances progress, or reuses deterministic evidence. Priority 4 therefore treats the current self-hosting and `finish-priority-05` workflows as compression and authority benchmarks before adding more execution topology.

### Immediate implementation order

1. **Runtime-owned phase lifecycle and recovery.** Replace procedural lifecycle/recovery YAML with safe runtime defaults plus explicit policy overrides.
2. **Engine-owned progress and completion bookkeeping.** Let deterministic acceptance advance declared progress and completion metadata instead of asking an agent to edit its own acceptance state.
3. **Content-addressed deterministic validation evidence.** Reuse a successful deterministic check when its tool definition, inputs, and relevant workspace identity are unchanged.
4. **Concise authoring surface and expanded plan.** Remove repeated configuration from authored YAML while preserving a fully inspectable normalized execution contract.

### Scope

#### Runtime-owned phase lifecycle and recovery

- Define a safe default lifecycle for mutable AI phases that covers clean-boundary checks, phase-start capture, durable actor-completion evidence, deterministic validation, checkpointing, completed-phase evidence, and active-state cleanup.
- Let workflows select concise lifecycle policy such as safe resume, accepted-phase checkpointing, and clean phase boundaries instead of spelling out each runtime operation.
- Derive normal interrupted-phase recovery from phase state and lifecycle policy rather than requiring a procedural `recovery` sequence in every workflow.
- Preserve explicit escape hatches for workflows that genuinely require non-default lifecycle behavior; defaults must never weaken authority or silently skip validation.
- Enforce workspace mutation policy, protected-resource integrity, branch lineage, and cleanliness continuously at relevant runtime mutation and acceptance boundaries so authors do not have to repeat scope/integrity assertions around every gate and checkpoint.
- Centralize equivalent lineage and safety declarations so the same invariant does not need parallel spelling under state, workspace, preconditions, validation, and completion.

#### Engine-owned progress and deterministic bookkeeping

- Reference progress criteria by stable IDs from phases instead of repeating the complete human-readable criterion text.
- Allow a criterion phase to declare that successful deterministic acceptance advances its targeted progress item; the phase actor should implement the criterion, not mark its own acceptance checkbox.
- Make the runtime enforce that only the targeted criterion advances and that the declared progress delta is satisfied.
- Add deterministic completion/bookkeeping operations for structured Markdown status, checklist, or index updates where the required transition is fully declarative.
- Remove bookkeeping-only model calls when the engine can perform the same constrained transition deterministically.
- Prefer structured Markdown-aware progress/bookkeeping semantics over shell normalization commands such as `sed` when protecting all non-bookkeeping content.

#### Deterministic validation evidence and reuse

- Make deterministic validation invocations produce durable evidence identified by the validation/tool definition, resolved non-secret inputs, and relevant workspace/content identity.
- Reuse successful evidence when a later workflow transition requires the same deterministic validation against an unchanged relevant workspace state.
- Invalidate cached evidence whenever relevant files, tool definitions, resolved inputs, policy, or other declared dependencies change.
- Preserve gate-specific repair budgets and failure classification; cached success must never convert a safety failure or stale validation into acceptance.
- Keep bounded failure logs available to repair actors without persisting unnecessary prompt, secret, or environment data.
- Exercise the canonical repository gate as the first content-addressed validation benchmark so post-audit and final transitions do not rerun identical work against the same tree.

#### Concise authoring model

- Add inherited agent/executor defaults so common runner, sandbox, approval, ephemeral, commit, and output settings are declared once.
- Add phase-kind and lifecycle defaults with clear overrides; common `criterion`, `implementation`, `audit`, and bookkeeping behavior should not require repeated boilerplate.
- Stabilize familiar declarative concepts such as `uses`, `with`, `if`, scoped `env`, and named references where they concretely reduce repeated YAML without blurring authority boundaries.
- Make repair configuration concise: a validation may name its bounded repair actor/policy and, by default, rerun the same deterministic validation after repair rather than repeating the validation step list.
- Keep temporary directories, log file naming, Git-ref record plumbing, and similar interpreter implementation details out of workflow YAML unless they are observable semantic requirements.
- Normalize parameter environment/default spelling onto the typed parameter model rather than embedding shell-style fallback expressions.
- Keep domain-specific phase prompts focused on the work itself; generic instructions such as marking criteria complete, preserving phase state, or obeying runtime-owned checkpoint semantics should not need prompt repetition.

#### Normalized executable plan

- Compile concise authoring syntax and runtime defaults into an explicit normalized workflow representation before execution.
- Add or extend `agentflow plan --expanded` so authors and reviewers can inspect the resolved lifecycle, recovery behavior, policy enforcement points, validation/repair behavior, progress transitions, and completion contract without reading interpreter source.
- Make validation operate on both the authoring document and the normalized executable representation so defaults cannot introduce unsupported or ambiguous behavior.
- Use the expanded representation as the basis for future semantic workflow comparison: authoring concision must not come at the cost of hidden behavior.

### Exit criteria

- `examples/finish-priority-05.agent-workflow.yaml` and the self-hosting workflow are materially smaller in non-prompt orchestration surface while preserving their existing mutation, validation, repair, recovery, human-gate, and completion guarantees.
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

## Priority 5 — Explicit dependency graph and scheduler

**Goal:** Move beyond a serialized phase list while retaining deterministic advancement and recovery.

### Scope

- Model execution dependencies explicitly.
- Build a ready-node scheduler for DAG execution.
- Support bounded parallel execution of independent nodes.
- Add fan-out/fan-in semantics.
- Define failure propagation, cancellation, skipped-node behavior, and downstream invalidation.
- Preserve deterministic validation and checkpoint rules when independent branches mutate different resources.
- Detect unsafe overlapping workspace mutation before concurrent execution.
- Persist scheduler state so interrupted parallel workflows resume safely.

### Exit criteria

- Independent read-only or disjoint work can execute concurrently.
- Dependencies, not YAML declaration order alone, determine readiness.
- A restart reconstructs the same executable graph and accepted-node state.
- Conflicting concurrent mutation is rejected or explicitly serialized.
- At least one self-hosted AgentFlow development workflow uses dependencies without weakening its mutation/checkpoint guarantees.

---

## Priority 6 — Typed contracts, artifacts, and evidence

**Goal:** Make handoffs between execution units machine-checkable rather than prompt conventions.

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

## Priority 7 — Executor and tool extensibility

**Goal:** Make the language portable across models, deterministic tools, humans, remote agents, and composite workflows.

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

## Priority 8 — Capabilities, credentials, approvals, and budgets

**Goal:** Move security and resource control out of prompt promises and into runtime-enforced policy.

### Scope

- Per-executor tool capabilities.
- Workspace and resource scopes beyond path allowlists.
- Network-access policy.
- Credential/secret scopes with redaction guarantees.
- Human approval requirements for privileged effects.
- Model-call, tool-call, token, time, repair, and optional monetary budgets.
- Cancellation and budget-exhaustion semantics.
- Policy inheritance and narrowing rules.
- Security-focused conformance fixtures, including prompt-injection-style attempts to exceed authority.

### Exit criteria

- An actor cannot gain a capability merely by asking for it in its prompt.
- Secrets are injected only into explicitly authorized execution contexts and are redacted from durable traces by default.
- Budget exhaustion produces a deterministic workflow state rather than uncontrolled retry behavior.

---

## Priority 9 — Execution trace, evidence, and observability

**Goal:** Make a run explainable independently of the reusable workflow definition.

### Scope

- Define a versioned execution-trace schema distinct from the workflow SDL.
- Assign stable run and node-execution identities.
- Record state transitions, attempts, validations, repairs, checkpoints, human gates, and completion evidence.
- Capture provider/tool metadata without requiring private model reasoning.
- Add `agentflow status` detail suitable for both humans and automation.
- Add `agentflow explain` for “why is this node blocked/skipped/failed?”
- Export structured run data for analysis and optional observability integrations.
- Track quality, latency, token/call counts, and cost where providers expose them.

### Exit criteria

- A completed or failed run can be reconstructed at the orchestration level from its trace and Git evidence.
- Users can explain why a node did or did not execute without reading interpreter logs.
- Workflow-definition data is not conflated with run-specific trace data.
- Self-hosted development runs provide enough trace data to diagnose failed or repaired phases without inspecting ad hoc shell output.

---

## Priority 10 — Reusable workflows and composition

**Goal:** Let teams build libraries of trusted procedures instead of copying prompt-heavy YAML.

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

## Priority 11 — Developer tooling and authoring experience

**Goal:** Make the SDL pleasant to author and safe to review.

### Scope

- `agentflow fmt` for canonical formatting where safe.
- `agentflow lint` for semantic and policy guidance beyond schema validity.
- `agentflow plan` to display the resolved execution graph without running it; Priority 4 establishes the expanded normalized-plan foundation this tooling builds on.
- Graph visualization for dependencies and gates.
- Shell/editor completion for CLI and workflow fields.
- JSON Schema or equivalent editor integration.
- High-quality examples covering sequential, conditional, recovery, DAG, human-gated, self-hosted, and reusable workflows.
- Semantic workflow comparison that distinguishes behavior changes from prompt-only text changes.
- Documentation generated from or verified against schema definitions where practical.

### Exit criteria

- A workflow author can validate, inspect, and graph a workflow before execution.
- Reviewers can identify authority, dependencies, and completion conditions without tracing interpreter code.
- Documentation and schema drift are caught in CI.

---

## Priority 12 — `v1beta1` stabilization and conformance

**Goal:** Establish a version suitable for real external workflows and independent interpreter implementations.

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

## Near-term sequence

The recommended implementation order is:

1. **Schema and diagnostics** — make the language mechanically precise.
2. **`v1alpha1` runtime parity** — close the documented/runtime gap.
3. **Self-hosting MVP** — use AgentFlow plus the Go interpreter to make a real, validated, resumable change to AgentFlow itself.
4. **Runtime-owned orchestration and concise authoring** — make lifecycle/recovery runtime-owned, move progress/bookkeeping authority into deterministic engine transitions, add content-addressed validation evidence, and expose the fully expanded execution contract.
5. **DAG scheduler** — add dependency-driven concurrency on top of concise, proven sequential semantics.
6. **Typed artifacts/evidence** — strengthen contracts between nodes beyond deterministic validation evidence.
7. **Extensibility and security** — broaden executors while keeping authority enforceable.
8. **Trace and composition** — make larger systems explainable and reusable.
9. **Developer tooling and `v1beta1`** — harden the ecosystem and compatibility contract.

The roadmap should be updated when implementation, research, or self-hosting experience materially changes these dependencies. The specification should not claim support for a roadmap item until both its SDL semantics and reference-interpreter behavior are covered by conformance tests.