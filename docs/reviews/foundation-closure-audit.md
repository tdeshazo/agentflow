# Execution stage 1 foundation-closure audit

Date: 2026-08-29

This audit compares the Stage 1 scope and exit criteria in `ROADMAP.md` with
checked-in specification material, runtime documentation, conformance fixtures,
implementation, tests, and observable CLI behavior. No external product
documentation was used. `ROADMAP.md` is treated as the audit target and is not
modified.

## Classification and method

Each row uses exactly one of these classifications:

- **Proven with exact repository evidence** — the behavior is backed by a
  source location plus a deterministic test or command.
- **Implementation/documentation gap** — the repository has no sufficient proof,
  or the public contract and implementation disagree or leave behavior
  ambiguous.
- **Explicitly unsupported surface consistent with the compatibility contract**
  — the construct is documented as outside this runtime and is rejected rather
  than silently ignored.

The most important distinction is between “the current runtime implements this”
and “Stage 1 has closure evidence for the whole public contract.” The former is
substantial; the latter is not yet complete.

## Deterministic baseline

The repository quality gate was run as:

```text
GOCACHE=/tmp/agentflow-audit-gocache ./scripts/check.sh
```

It passed formatting, diff hygiene, ordinary tests, vet, race-enabled tests,
the CLI build, and the reference-definition validation. The engine tests passed
in 176.347s normally and 204.364s with the race detector. The build emitted
only the environment-specific read-only Go module stat-cache warning; the gate
completed successfully.

The gate implementation is [scripts/check.sh](../../scripts/check.sh), and its
scope is described in [README.md](../../README.md). The test and race commands
were given a writable temporary `GOCACHE`; no repository source or protected
path was changed.

Additional observable CLI checks used the built binary:

```text
agentflow validate -f spec/agent-workflow-v1alpha1.yaml
agentflow validate -f examples/art-portfolio.agent-workflow.yaml
agentflow validate -f examples/feature.agent-workflow.yaml
agentflow validate -f internal/workflow/testdata/conformance/valid/concise-defaults.yaml
agentflow validate -f internal/workflow/testdata/conformance/valid/minimal.yaml
agentflow validate -f internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml
```

All six returned `valid and executable`. `agentflow plan --expanded` also
returned deterministic YAML for the reference workflow and both shipped
examples. The CLI validates before constructing an engine: see
`internal/agentflowcli/main.go:351-375`.

## Stage 1A — executable schema, validation, and diagnostics

### Scope

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1A-S1 | Define a machine-readable schema for executable `v1alpha1`. | **Implementation/documentation gap** | `internal/workflow/model.go:10-12` calls the Go model authoritative and `model.go:604-615` enables strict YAML fields, but the repository has no checked-in standalone schema artifact under `schema/`. The public reference explicitly calls itself descriptive and not an externally standardized schema in `docs/reference/agentflow-v1alpha1.md:1-9`. Check: inventory `rg --files schema` and compare it with the model and reference. |
| 1A-S2 | Add `agentflow validate` for syntax, type, reference, and structural validation. | **Proven with exact repository evidence** | `internal/workflow/load.go:76-181` decodes and validates files; `internal/workflow/validate.go:13-69` performs authored and normalized validation; `internal/agentflowcli/main.go:351-353` exposes it. Check: run the six `validate` commands above and the conformance suite in `internal/workflow/conformance_test.go:12-30`. |
| 1A-S3 | Validate IDs and references across actors, tools, validations, phases, progress, human gates, and completion blocks. | **Proven with exact repository evidence** | Cross-reference checks are in `internal/workflow/validate.go:284-432`; representative invalid references are asserted in `internal/workflow/conformance_test.go:111-141` and `internal/workflow/validate_test.go:108-145`. Check: run `go test ./internal/workflow -run 'Conformance|Reference|Cross'` with the temporary cache. |
| 1A-S4 | Distinguish descriptive fields from executable fields explicitly. | **Implementation/documentation gap** | The implementation comments distinguish `Metadata` from executable `Spec` in `internal/workflow/model.go:14-63`, and the runtime rejects unsupported enforcement fields in `docs/reference/runtime.md:6-10`. However, the public field guide says it is descriptive (`docs/reference/agentflow-v1alpha1.md:3`) and does not provide a complete field-by-field executable/descriptive contract. `allowed_semantic_changes` is accepted by the model at `internal/workflow/model.go:200-202` but has no runtime implementation or canonical field-guide entry. Check: compare all YAML tags in `model.go` with the reference tables and run a workflow containing `allowed_semantic_changes`. |
| 1A-S5 | Produce source-aware diagnostics with YAML path and line/column where practical. | **Proven with exact repository evidence** | `internal/workflow/load.go:15-18,50-60,183-214` stores paths and positions; stable path/message/position behavior is tested in `internal/workflow/conformance_test.go:111-158`; CLI formatting is exercised through `internal/agentflowcli/main.go:572-590`. Check: run the invalid conformance corpus repeatedly and compare diagnostics. |
| 1A-S6 | Reject unknown executable fields by default. | **Proven with exact repository evidence** | Strict decoding is explicit in `internal/workflow/load.go:76-136` and `internal/workflow/model.go:604-615`; `unknown-executable-field.yaml` is asserted invalid in `internal/workflow/conformance_test.go:111-125`. Check: `agentflow validate -f internal/workflow/testdata/conformance/invalid/unknown-executable-field.yaml`. |
| 1A-S7 | Add positive and negative conformance fixtures. | **Proven with exact repository evidence** | Positive and unsupported cases are enumerated in `internal/workflow/conformance_test.go:12-30`; invalid cases and expected paths/messages are enumerated at `:111-141`. The fixtures are under `internal/workflow/testdata/conformance/{valid,invalid,unsupported}`. Check: `go test ./internal/workflow -run Conformance`. |
| 1A-S8 | Document the compatibility promise within an API version. | **Proven with exact repository evidence** | The runtime contract says implemented constructs execute and unsupported constructs fail closed in `docs/reference/runtime.md:3-10,517-523,606-617`; the v1alpha1/v1alpha2 separation and no-silent-change promise are stated in `README.md:155-173`. Check: validate the unsupported runtime-surface fixture and inspect the CLI status before execution. |

### Exit criteria

| ID | Exit criterion | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1A-E1 | Invalid workflow references fail before workspace mutation. | **Proven with exact repository evidence** | `internal/agentflowcli/main.go:351-375` validates and rejects invalid/unsupported documents before `engine.New`; `internal/workflow/conformance_test.go:111-141` proves invalid references. Check: run an invalid fixture against a temporary Git repository and verify no engine state is created. |
| 1A-E2 | Reference workflow and shipped examples validate cleanly. | **Proven with exact repository evidence** | `TestConformanceShippedDefinitions` checks the reference and feature example in `internal/workflow/conformance_test.go:160-170`; the six direct CLI checks above all returned `valid and executable`; `scripts/check.sh:48-54` validates the reference definition. |
| 1A-E3 | Conformance distinguishes supported, unsupported, and invalid constructs. | **Proven with exact repository evidence** | `internal/workflow/conformance_test.go:12-30` expects executable and unsupported statuses; `:111-141` expects invalid status and diagnostics. Check: `go test ./internal/workflow -run Conformance`. |
| 1A-E4 | Interpreter and documentation share one authoritative executable schema. | **Implementation/documentation gap** | The Go model/strict decoder is authoritative for interpretation (`internal/workflow/model.go:10-12`), but the checked-in public reference is explicitly descriptive and no standalone schema is checked in. The model also accepts the undocumented/unimplemented `allowed_semantic_changes` field. This prevents claiming a single public executable schema. |

## Stage 1B — `v1alpha1` runtime parity

### Scope

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1B-S1 | Complete documented precondition, assertion, validation, checkpoint, progress, recovery, human-gate, and completion semantics. | **Proven with exact repository evidence** | The supported core is enumerated in `docs/reference/runtime.md:525-559`; implementation is covered by `internal/engine/runtime_phase.go`, `runtime_human.go`, `runtime_checkpoint.go`, `validation_evidence.go`, and the durability, human, policy, and validation-evidence tests. Check: the full `scripts/check.sh` gate. |
| 1B-S2 | Expand the expression evaluator into a deliberately bounded expression model. | **Proven with exact repository evidence** | The bounded grammar and fail-closed behavior are documented in `docs/reference/runtime.md:561-566`; parser/evaluator rejection paths are in `internal/workflow/template.go:769-786`, with malformed-expression and invalid-expression fixtures. Check: `go test ./internal/workflow -run 'Expression|Template'`. |
| 1B-S3 | Implement dynamic bounded loops such as “next unchecked criterion.” | **Proven with exact repository evidence** | `internal/engine/runtime_loop.go:10-68` dispatches the stable next criterion and enforces a one-item delta; `internal/engine/control_flow_test.go:106-132` tests stable-ID dispatch. Check: `go test ./internal/engine -run 'Loop|ControlFlow'`. |
| 1B-S4 | Support conditional execution for phases, gates, and steps. | **Proven with exact repository evidence** | Conditional flow is listed in `docs/reference/runtime.md:555-557`; execution is implemented in `internal/engine/runtime_phase.go` and `runtime_human.go`, with conditions tested in `internal/engine/control_flow_test.go:37-104`. Check: `go test ./internal/engine -run 'Condition|ControlFlow|Human'`. |
| 1B-S5 | Make parameter typing, defaults, environment values, and override errors consistent. | **Proven with exact repository evidence** | Typed resolution is implemented in `internal/engine/engine.go:350-410` and normalized in `internal/workflow/normalize.go`; `internal/engine/parameters_test.go:10-94` covers defaults, environment values, CLI overrides, unknown overrides, bad types, and cycles. |
| 1B-S6 | Make recovery idempotent across partial commits, dirty state, completed criteria, and interrupted validation. | **Proven with exact repository evidence** | Recovery ordering and no-replay rules are documented in `docs/reference/runtime.md:363-379`; durable seams are covered by `internal/engine/durability_test.go` (partial work, `actor_completed`, repair, safety terminal, and marker cases) and `internal/engine/runtime_phase_conformance_test.go`. Check: `go test ./internal/engine -run 'Durability|Recovery|Pending|Conformance'`. |
| 1B-S7 | Verify lineage under rebases, detached commits, branch changes, and invalidated phase markers. | **Proven with exact repository evidence** | Runtime lineage enforcement is in `internal/engine/runtime_policy.go:131-283`; the invalidated-marker, branch, detached, orphan, and non-ancestor cases are in `internal/engine/durability_test.go:1181-1264`. Check: `go test ./internal/engine -run 'Lineage|Marker|Detached|Branch'`. |
| 1B-S8 | Remove interpreter behavior that exists only for the current reference workflow unless generalized into the specification. | **Implementation/documentation gap** | Legacy `phaseDefaults`, phase `after`, and `recovery.activePhase` remain accepted as v1alpha1 escape hatches (`docs/reference/runtime.md:354-361`), while the reference YAML still contains procedural recovery and log plumbing at `spec/agent-workflow-v1alpha1.yaml:146,338-388`. The repository documents the compatibility behavior but does not provide an inventory proving every reference-only behavior is generalized. Check: compare the reference YAML’s procedural fields with the public field guide and normalized plan. |

### Exit criteria

| ID | Exit criterion | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1B-E1 | Every executable construct documented as v1alpha1 is implemented or explicitly non-executable. | **Proven with exact repository evidence** | The implemented list is in `docs/reference/runtime.md:525-559`; explicit limits and fail-closed rejection are in `:606-617`; validator rejection paths are in `internal/workflow/validate.go:216-220,434-499,501-675`. Check: run the valid, invalid, and unsupported conformance corpus. |
| 1B-E2 | The existing shell-orchestrated reference workflow can be expressed and completed through AgentFlow without semantic loss. | **Implementation/documentation gap** | The reference workflow validates and can be planned, but checked-in evidence only validates definitions (`internal/workflow/conformance_test.go:160-170`) and does not complete `spec/agent-workflow-v1alpha1.yaml` end-to-end through a provider. Its actor-owned progress and bookkeeping are also contrary to the Stage 1C authority target (see 1C-S13 and 1C-S19). Check: a deterministic provider-backed completion fixture for the reference workflow is missing. |
| 1B-E3 | Restarting at any durable phase boundary does not require replaying accepted work. | **Proven with exact repository evidence** | Recovery ordering is specified in `docs/reference/runtime.md:363-379`; actor-completion and pending-invocation crash windows are tested in `internal/engine/durability_test.go` and `runtime_phase_conformance_test.go`. Check: `go test ./internal/engine -run 'Durability|Pending|Recovery'` and the race-enabled gate. |

## Stage 1C — runtime-owned orchestration and concise SDL

### Scope: runtime-owned phase lifecycle and recovery

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-RT1 | Safe default lifecycle covers clean checks, phase-start capture, actor-completion evidence, deterministic validation, checkpoint, completed evidence, and cleanup. | **Proven with exact repository evidence** | `internal/engine/runtime_lifecycle.go:12-85` defines the actions and `:88-287` enforces them at boundaries; the ordered contract is documented in `docs/reference/runtime.md:320-352`. Check: `go test ./internal/engine -run 'Lifecycle|Durability'`. |
| 1C-RT2 | Concise lifecycle policy selects safe resume, accepted checkpointing, and clean boundaries. | **Proven with exact repository evidence** | `docs/reference/runtime.md:322-326,354-361` defines `safe-resume` and defaults; `internal/workflow/normalize.go` and `internal/workflow/normalize_test.go:184-228` test normalized defaults. Check: `agentflow plan --expanded -f internal/workflow/testdata/conformance/valid/concise-defaults.yaml`. |
| 1C-RT3 | Normal interrupted recovery derives from phase state and policy rather than requiring procedural recovery. | **Proven with exact repository evidence** | `docs/reference/runtime.md:363-379` describes state/policy recovery; `internal/engine/runtime_phase.go:300-449` and durability tests cover marker, pending, and `actor_completed` recovery. Check: run the interruption/restart tests with a concise fixture containing no `recovery.activePhase`. |
| 1C-RT4 | Explicit escape hatches remain, but defaults never weaken authority or skip validation. | **Proven with exact repository evidence** | Legacy escape hatches and fixed safety properties are documented in `docs/reference/runtime.md:354-361`; the boundary is asserted around actions in `internal/engine/runtime_lifecycle.go:88-287`. Check: `go test ./internal/engine -run 'Safety|Lifecycle|Authority'`. |
| 1C-RT5 | Mutation, integrity, lineage, and cleanliness are enforced continuously at runtime boundaries. | **Proven with exact repository evidence** | Shared checks are in `internal/engine/runtime_policy.go:131-283`; the runtime invokes them before/after work and acceptance as documented in `docs/reference/runtime.md:393-397`; policy tests cover paths, hashes, ignored files, and symlinks. Check: `go test ./internal/engine -run 'Policy|Integrity|Lineage|Boundary'`. |
| 1C-RT6 | Equivalent lineage and safety declarations are centralized rather than repeated under state, workspace, preconditions, validation, and completion. | **Implementation/documentation gap** | Runtime enforcement is centralized, but the model still exposes lineage and safety declarations in multiple sections (`internal/workflow/model.go:95-216`), and `internal/engine/runtime_policy.go` combines those sources. The reference workflow repeats scope/state/precondition/completion assertions. The repository proves shared enforcement, not the requested single declaration. |

### Scope: engine-owned progress and deterministic bookkeeping

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-PG1 | Reference progress criteria by stable IDs instead of repeating complete text. | **Proven with exact repository evidence** | `criterionID` is modeled at `internal/workflow/model.go:415-438`; normalization and dispatch are in `internal/workflow/normalize.go:94-107`; stable-ID conformance is tested in `internal/engine/control_flow_test.go:106-132`. |
| 1C-PG2 | Criterion acceptance can advance its target while the actor implements, not marks, the criterion. | **Proven with exact repository evidence** | `internal/engine/runtime_lifecycle.go:163-183` advances after acceptance; actor progress edits are prohibited by `internal/engine/actor_quarantine.go:116`; the public contract is `docs/reference/runtime.md:345-349`. Check: `go test ./internal/engine -run 'Progress|ActorBoundary'`. |
| 1C-PG3 | Enforce only the targeted criterion and the declared delta. | **Proven with exact repository evidence** | `internal/engine/runtime_loop.go:41-66` and `runtime_checkpoint.go:409+` enforce target/delta; `internal/engine/control_flow_test.go:106-132` tests the violation. |
| 1C-PG4 | Deterministic structured Markdown status/checklist/index updates. | **Proven with exact repository evidence** | `internal/engine/markdown_bookkeeping.go:1-334` implements constrained transitions; `internal/engine/markdown_bookkeeping_test.go:106-270` covers actorlessness, exact target, preservation, recovery, external edits, and boundaries. |
| 1C-PG5 | Remove bookkeeping-only model calls when deterministic engine transitions suffice. | **Proven with exact repository evidence** | Engine-owned bookkeeping rejects an actor and is tested at `internal/engine/markdown_bookkeeping_test.go:111-129`; the validator requires no actor at `internal/workflow/validate.go:317-319`; the runtime documents actor-less transitions at `docs/reference/runtime.md:537-540`. The shipped legacy reference is not yet migrated; that is an exit/representative-workflow gap, not a missing engine capability. |
| 1C-PG6 | Prefer structured Markdown-aware semantics over `sed`-style shell normalization. | **Proven with exact repository evidence** | Structured transition parsing and byte preservation are implemented in `internal/engine/markdown_bookkeeping.go`; preservation and unauthorized-change tests are in `internal/engine/markdown_bookkeeping_test.go:167-270`; the field guide defines the same constraints at `docs/reference/agentflow-v1alpha1.md:376-383`. |

### Scope: deterministic validation evidence and reuse

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-EV1 | Durable evidence is keyed by validation/tool definitions, resolved non-secret inputs, and workspace identity. | **Proven with exact repository evidence** | Key construction is in `internal/engine/validation_evidence.go:134-174`; the runtime lists content-addressed evidence in `docs/reference/runtime.md:545-549`. Check: `go test ./internal/engine -run ValidationEvidence`. |
| 1C-EV2 | Reuse successful evidence for the same deterministic validation and unchanged relevant state. | **Proven with exact repository evidence** | Reuse is implemented in `internal/engine/runtime_phase.go:758-933`; restart/reuse is tested in `internal/engine/validation_evidence_test.go:117+`. |
| 1C-EV3 | Invalidate evidence on relevant files, tool definitions, inputs, policy, or declared dependency changes. | **Proven with exact repository evidence** | The evidence key includes those inputs in `validation_evidence.go`; changed-state invalidation is tested by `internal/engine/validation_evidence_test.go`. Check: `go test ./internal/engine -run 'ValidationEvidence|Invalidat'`. |
| 1C-EV4 | Preserve gate-specific repair budgets and failure classification; cached success cannot bypass safety/staleness. | **Proven with exact repository evidence** | Repair and safety ordering are in `internal/engine/runtime_phase.go:758-933`; bounded/redacted failures and safety-terminal behavior are tested in `validation_evidence_test.go` and `durability_test.go`. |
| 1C-EV5 | Keep bounded failure logs available to repair actors without unnecessary prompt, secret, or environment data. | **Proven with exact repository evidence** | Failure capture/redaction is implemented in `internal/engine/runtime_phase.go:758-933` and documented in `docs/reference/runtime.md:543-549`; validation evidence tests assert bounded/redacted records. |
| 1C-EV6 | Exercise the canonical repository gate as the first content-addressed validation benchmark. | **Implementation/documentation gap** | Generic validation-evidence reuse is proven, but `scripts/check.sh` only runs the repository gate and validates the reference definition (`scripts/check.sh:1-58`); no checked-in fixture runs the canonical gate twice through the content-addressed evidence path and proves the second run is reused. Check: add/run a deterministic gate-backed evidence test; it does not currently exist. |

### Scope: concise authoring model

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-AU1 | Inherited agent/executor defaults for common authority settings. | **Proven with exact repository evidence** | Default merging is in `internal/workflow/normalize.go`; strict concise lowering and default tests are in `internal/workflow/authoring_syntax.go` and `authoring_syntax_test.go`; normalized capability assertions are in `internal/workflow/conformance_test.go:32-68`. |
| 1C-AU2 | Phase-kind and lifecycle defaults with clear overrides. | **Proven with exact repository evidence** | `internal/workflow/normalize.go` compiles phase/lifecycle defaults; `internal/workflow/normalize_test.go:184-228` and `plan_test.go:13-36` assert safe-resume and acceptance behavior. |
| 1C-AU3 | Familiar `uses`, `with`, `if`, scoped `env`, and named references where useful. | **Proven with exact repository evidence** | Concise lowering and strict-field behavior are covered in `docs/guides/concise-authoring.md:8-91,207-276` and `internal/workflow/authoring_syntax_test.go`; conditional/named-reference validation is in `internal/workflow/validate.go`. |
| 1C-AU4 | Concise repair policy reruns the same deterministic gate. | **Proven with exact repository evidence** | Repair normalization is tested in `internal/workflow/conformance_test.go:62-68`; execution and one-attempt durability are tested in `internal/engine/durability_test.go` and `validation_evidence_test.go`; the authoring rule is documented in `docs/guides/concise-authoring.md:55-68`. |
| 1C-AU5 | Keep temporary directories, log naming, Git-ref plumbing, and similar implementation details out of YAML unless observable. | **Implementation/documentation gap** | Concise fixtures omit these details, but the canonical v1alpha1 reference still contains a log path at `spec/agent-workflow-v1alpha1.yaml:146` and procedural recovery at `:338-388`. The runtime supports compatibility fields, but there is no checked-in closure rule separating observable policy from implementation detail across the whole v1alpha1 surface. |
| 1C-AU6 | Normalize parameter environment/default spelling onto typed parameters. | **Proven with exact repository evidence** | Typed parameter normalization is implemented in `internal/workflow/normalize.go` and exercised by `internal/engine/parameters_test.go:10-94`; the supported model is documented in `docs/reference/runtime.md:525-529`. |
| 1C-AU7 | Keep domain prompts focused; omit generic acceptance/state/checkpoint instructions. | **Implementation/documentation gap** | The runtime-owned prompt boundary exists in `internal/engine/runtime_phase.go:476-497`, but the shipped reference prompt explicitly tells the actor to mark acceptance at `spec/agent-workflow-v1alpha1.yaml:240-268` and gives bookkeeping instructions at `:299-309`. This is a concrete authoring-contract violation even though the runtime enforces the boundary. |

### Scope: normalized executable plan

| ID | Scope item | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-PL1 | Compile concise syntax and runtime defaults into an explicit normalized representation. | **Proven with exact repository evidence** | `internal/workflow/normalize.go` performs the lowering; `internal/workflow/validate.go:13-45` validates and retains the normalized document; `internal/workflow/conformance_test.go:32-109` checks normalized actors, tools, repair, graph, and deterministic plan output. |
| 1C-PL2 | `plan --expanded` exposes lifecycle, recovery, policy, validation/repair, progress, and completion behavior. | **Implementation/documentation gap** | The command path is proven at `internal/agentflowcli/main.go:358-372`; plan fields and generated acceptance are in `internal/workflow/plan.go:9-185`. However, the plan does not expose the complete normalized state/precondition/integrity/cleanliness matrix, flow conditions, or all legacy action details that influence execution. The CLI succeeds, but this exit criterion requires a complete inspectable contract. |
| 1C-PL3 | Validate authored and normalized representations. | **Proven with exact repository evidence** | `internal/workflow/validate.go:13-45` validates both authored and normalized workflows; `internal/workflow/conformance_test.go:32-109` proves normalization and plan behavior. |
| 1C-PL4 | Use the expanded representation as the basis for future semantic comparison without hidden behavior. | **Implementation/documentation gap** | `internal/workflow/plan.go:9-25` provides a useful stable projection and `conformance_test.go:96-108` proves deterministic YAML, but omitted normalized execution fields mean it is not yet a complete comparison basis. Check: compare the expanded plan against all normalized fields in `internal/workflow/model.go`. |

### Exit criteria

| ID | Exit criterion | Classification | Exact evidence and deterministic check |
| --- | --- | --- | --- |
| 1C-E1 | Representative workflows need no explicit procedural active-phase recovery for normal safe resume. | **Proven with exact repository evidence** | `internal/workflow/testdata/conformance/valid/concise-defaults.yaml` and `examples/art-portfolio.agent-workflow.yaml` use lifecycle defaults; `docs/reference/runtime.md:322-326,363-379` defines derived recovery. Check: `agentflow plan --expanded` on both files and inspect that safe-resume is resolved without a procedural active-phase sequence. |
| 1C-E2 | Workspace scope, protected integrity, lineage, and cleanliness are declared once and enforced at every required boundary. | **Implementation/documentation gap** | Enforcement at every boundary is proven by `docs/reference/runtime.md:393-397` and `runtime_policy.go`; declaration-once is not: `internal/workflow/model.go:95-216` and the reference YAML expose/repeat related state, workspace, precondition, validation, and completion declarations. |
| 1C-E3 | Criterion prompts do not ask an agent to mark its own acceptance criterion complete. | **Implementation/documentation gap** | Runtime enforcement is proven by `actor_quarantine.go:116` and lifecycle acceptance, but the shipped reference prompt does ask this at `spec/agent-workflow-v1alpha1.yaml:249,264`. Compatibility does not make the representative shipped workflow satisfy the new authoring exit. |
| 1C-E4 | Deterministic completion bookkeeping no longer consumes an AI phase where expressible. | **Implementation/documentation gap** | Actor-less deterministic bookkeeping is implemented and tested (`internal/engine/markdown_bookkeeping_test.go:111-129`), but the shipped reference still defines an actor-backed bookkeeping phase at `spec/agent-workflow-v1alpha1.yaml:299-309`. A migrated representative workflow or explicit compatibility exception is missing. |
| 1C-E5 | Requiring the same gate twice reuses valid evidence; changing a dependency reruns it. | **Proven with exact repository evidence** | Keying/reuse/invalidation are implemented in `internal/engine/validation_evidence.go` and `runtime_phase.go`; tests cover restart reuse and changed state in `internal/engine/validation_evidence_test.go`. |
| 1C-E6 | Common agents, lifecycle behavior, and repair policy use defaults/references. | **Proven with exact repository evidence** | Concise defaults and repair normalization are tested in `internal/workflow/authoring_syntax_test.go`, `normalize_test.go`, and `conformance_test.go:32-109`; `docs/guides/concise-authoring.md:8-91` documents the authoring form. |
| 1C-E7 | `plan --expanded` exposes all runtime-generated lifecycle, recovery, validation, progress, checkpoint, and completion behavior before execution. | **Implementation/documentation gap** | The plan exposes a documented subset (`internal/workflow/plan.go:62-185`) and the CLI builds it before the engine (`internal/agentflowcli/main.go:358-372`), but it omits several normalized policy and procedural fields that can affect execution. The available output is not proof of “all.” |
| 1C-E8 | A representative workflow reads primarily as domain policy and work intent. | **Implementation/documentation gap** | Concise examples demonstrate the direction (`docs/guides/concise-authoring.md:8-91`), but the shipped v1alpha1 reference includes runtime log plumbing, recovery sequence, actor-owned acceptance, and bookkeeping (`spec/agent-workflow-v1alpha1.yaml:146,240-309,338-388`). There is no checked-in equivalence/concision benchmark establishing closure. |
| 1C-E9 | Actor, mutation, and deterministic-validation authority remain structurally distinct in concise and expanded forms. | **Proven with exact repository evidence** | Actor, workspace mutation, and validation are separate model/plan fields in `internal/workflow/model.go:65-216,218-337` and `internal/workflow/plan.go:28-60`; runtime authority enforcement is tested in `internal/engine/runtime_phase_test.go` and `invocation_may_commit_test.go`. |
| 1C-E10 | Incompatible grammar changes use a new alpha API version with an explicit migration path. | **Implementation/documentation gap** | Separate v1alpha1/v1alpha2 contracts and no silent v1alpha1 reinterpretation are documented in `README.md:155-173` and `docs/reference/agentflow-v1alpha2.md:1-24`. A user-facing migration path is not present; repository migration references are implementation compatibility tests and later-roadmap material, not an author migration guide or command. Check: `rg -n "migration|v1alpha1.*v1alpha2|v1alpha2.*v1alpha1" README.md docs skills spec internal` and verify no migration procedure exists. |

## Closure decision

Stage 1 is **not closed**. The repository proves a substantial executable core:
strict pre-execution validation, deterministic diagnostics, bounded expressions,
conditional flow, typed parameters, durable safe-resume behavior, lineage and
integrity boundaries, engine-owned progress/bookkeeping, content-addressed
validation evidence, concise normalization, and an observable expanded plan.

The closure blockers are concrete rather than inferred from later milestones:

1. There is no standalone checked-in public machine-readable executable schema,
   and the model/reference contract has at least one accepted but undocumented
   and unenforced field (`allowed_semantic_changes`).
2. The canonical v1alpha1 reference remains procedural in the exact areas Stage
   1C is meant to move into runtime ownership: actor-owned criterion marking,
   actor-backed bookkeeping, recovery plumbing, and runtime log details.
3. `plan --expanded` is deterministic and useful but not a complete projection
   of all normalized execution-affecting policy.
4. The repository lacks deterministic end-to-end completion evidence for the
   shell-orchestrated reference workflow and lacks a canonical gate-backed
   content-addressed benchmark.
5. The API-version separation is documented, but an explicit v1alpha1-to-new-
   alpha migration path is not.

These findings are limited to checked-in repository evidence and do not change
runtime behavior or the roadmap.
