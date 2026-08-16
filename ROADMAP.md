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

The current runtime intentionally implements a conservative subset of the broader descriptive specification. The roadmap closes that gap before expanding the language substantially.

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

## Priority 3 — GitHub-Actions-like SDL authoring model

**Goal:** Make non-trivial workflows concise, regular, and learnable without weakening AgentFlow's authority model.

### Scope

Evaluate and stabilize familiar declarative concepts such as:

- `needs` for explicit dependencies;
- `if` for conditions;
- `uses` for reusable tools, actions, or workflow fragments;
- `with` for typed inputs;
- `env` for scoped environment values;
- named outputs and output references;
- defaults inherited by execution units;
- concise step syntax for deterministic operations;
- clear separation between AI execution units and deterministic steps.

The language should avoid carrying forward accidental complexity from the original shell workflows. If the improved grammar requires incompatible field changes, introduce a new alpha API version with a documented migration rather than silently changing `v1alpha1` semantics.

### Exit criteria

- A representative workflow can be read top-to-bottom without consulting runtime implementation details.
- Common orchestration patterns require materially less repeated YAML than the initial reference format.
- Actor authority, mutation authority, and validation authority remain structurally distinguishable.
- A migration path exists for any superseded `v1alpha1` syntax.

---

## Priority 4 — Explicit dependency graph and scheduler

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

---

## Priority 5 — Typed contracts, artifacts, and evidence

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

---

## Priority 6 — Executor and tool extensibility

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

## Priority 7 — Capabilities, credentials, approvals, and budgets

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

## Priority 8 — Execution trace, evidence, and observability

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

---

## Priority 9 — Reusable workflows and composition

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

---

## Priority 10 — Developer tooling and authoring experience

**Goal:** Make the SDL pleasant to author and safe to review.

### Scope

- `agentflow fmt` for canonical formatting where safe.
- `agentflow lint` for semantic and policy guidance beyond schema validity.
- `agentflow plan` to display the resolved execution graph without running it.
- Graph visualization for dependencies and gates.
- Shell/editor completion for CLI and workflow fields.
- JSON Schema or equivalent editor integration.
- High-quality examples covering sequential, conditional, recovery, DAG, human-gated, and reusable workflows.
- Semantic workflow comparison that distinguishes behavior changes from prompt-only text changes.
- Documentation generated from or verified against schema definitions where practical.

### Exit criteria

- A workflow author can validate, inspect, and graph a workflow before execution.
- Reviewers can identify authority, dependencies, and completion conditions without tracing interpreter code.
- Documentation and schema drift are caught in CI.

---

## Priority 11 — `v1beta1` stabilization and conformance

**Goal:** Establish a version suitable for real external workflows and independent interpreter implementations.

### Scope

- Resolve accumulated alpha design decisions.
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

---

## Later research directions

These are deliberately not on the critical path to a stable declarative runtime, but the SDL should avoid designs that make them impossible:

- dynamic manager/worker routing;
- search/tree-based execution strategies within bounded nodes;
- workflow optimization and candidate comparison;
- procedural workflow memory;
- formal verification backends;
- distributed schedulers and remote execution;
- caching and content-addressed deterministic steps;
- policy-as-code integrations; and
- learned orchestration policies constrained by declarative authority.

## Near-term sequence

The recommended implementation order is:

1. **Schema and diagnostics** — make the language mechanically precise.
2. **`v1alpha1` runtime parity** — close the documented/runtime gap.
3. **SDL authoring model** — simplify and stabilize the YAML surface before adding more topology.
4. **DAG scheduler** — add dependency-driven concurrency on top of stable semantics.
5. **Typed artifacts/evidence** — strengthen contracts between nodes.
6. **Extensibility and security** — broaden executors while keeping authority enforceable.
7. **Trace and composition** — make larger systems explainable and reusable.
8. **Developer tooling and `v1beta1`** — harden the ecosystem and compatibility contract.

The roadmap should be updated when implementation or research materially changes these dependencies. The specification should not claim support for a roadmap item until both its SDL semantics and reference-interpreter behavior are covered by conformance tests.
