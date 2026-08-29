# Execution stage 1 foundation closure

Date: 2026-08-29  
Evidence baseline: repository `HEAD` `0c475fc847edc9083822736782becdb340a5a458`

This is the durable closeout record for execution stage 1 in
[`ROADMAP.md`](../../ROADMAP.md). It uses only checked-in repository sources,
documentation, fixtures, tests, and commands. `ROADMAP.md` is evidence input
only and was not modified.

The detailed review remains in
[`docs/reviews/foundation-closure-audit.md`](../reviews/foundation-closure-audit.md).
This record is the criterion-by-criterion closeout index. Paths below are
relative to the repository root unless a link makes their location explicit;
commands are run from the repository root.

## Decision and evidence method

Stage 1 is **closed**. Every Stage 1A, 1B, and 1C exit criterion below is
classified as either implemented and proven by repository evidence, or
explicitly non-executable and rejected before execution. No later-stage work is
counted as complete here.

Each criterion records four evidence kinds:

- **Artifacts** — checked-in specification, implementation, documentation, or
  fixture that defines or implements the behavior.
- **Conformance** — a named test or fixture that checks the public boundary.
- **Command** — a deterministic command that reproduces the check.
- **Recovery** — the relevant restart, durability, or pre-execution safety
  proof; `not applicable` is stated where the criterion is only about parsing
  or planning.

## Stage 1A — Executable schema, validation, and diagnostics

### 1A-E1 — Invalid references fail before workspace mutation

Status: **proven**.

- **Artifacts:** [`internal/agentflowcli/main.go`](../../internal/agentflowcli/main.go)
  validates before engine construction; the invalid reference fixture is
  [`unknown-references.yaml`](../../internal/workflow/testdata/conformance/invalid/unknown-references.yaml).
- **Conformance:**
  [`TestValidateCLIRejectsInvalidReferencesBeforeRepositoryAccess`](../../internal/agentflowcli/main_test.go)
  invokes validation with a non-existent repository and still receives the
  source-aware reference diagnostic.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/agentflowcli -run '^TestValidateCLIRejectsInvalidReferencesBeforeRepositoryAccess$' -count=1`
- **Recovery:** Not applicable to an invalid document. The test's failure
  before repository access is the relevant safety evidence: no engine or
  workspace mutation is possible.

### 1A-E2 — Reference workflow and shipped examples validate cleanly

Status: **proven**.

- **Artifacts:** the checked-in reference workflow
  [`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml)
  and shipped examples
  [`art-portfolio.agent-workflow.yaml`](../../examples/art-portfolio.agent-workflow.yaml)
  and [`feature.agent-workflow.yaml`](../../examples/feature.agent-workflow.yaml).
- **Conformance:**
  [`TestConformanceShippedDefinitions`](../../internal/workflow/conformance_test.go)
  validates those files; the quality gate validates every checked-in YAML file
  below `spec/` and `examples/` through the built CLI.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow -run '^TestConformanceShippedDefinitions$' -count=1`
  and `GOCACHE=/tmp/agentflow-foundation-gocache ./scripts/check.sh`.
- **Recovery:** Not applicable to validation-only checks. The reference
  workflow's end-to-end restart evidence is recorded under 1B-E2 and 1B-E3.

### 1A-E3 — Conformance distinguishes supported, unsupported, and invalid constructs

Status: **proven**.

- **Artifacts:** positive fixtures in
  [`testdata/conformance/valid`](../../internal/workflow/testdata/conformance/valid),
  invalid fixtures in
  [`testdata/conformance/invalid`](../../internal/workflow/testdata/conformance/invalid),
  and explicitly unsupported fixtures in
  [`testdata/conformance/unsupported`](../../internal/workflow/testdata/conformance/unsupported).
- **Conformance:**
  [`TestConformanceCorpus`](../../internal/workflow/conformance_test.go)
  asserts `executable` and `unsupported` outcomes; 
  [`TestConformanceInvalidDiagnostics`](../../internal/workflow/conformance_test.go)
  asserts invalid status, YAML paths, and messages; 
  [`TestConformanceDiagnosticOrderIsStable`](../../internal/workflow/conformance_test.go)
  checks deterministic diagnostic order.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow -run '^TestConformance' -count=1`
- **Recovery:** Unsupported and invalid fixtures are rejected before an engine
  is constructed. The pre-execution rejection is independently covered for
  authored active-phase recovery by
  [`TestValidateCLIRejectsNonExecutableRecoveryBeforeRepositoryAccess`](../../internal/agentflowcli/main_test.go).

### 1A-E4 — Interpreter and documentation share one authoritative executable schema

Status: **proven**.

- **Artifacts:** generated schema production in
  [`internal/workflow/schema.go`](../../internal/workflow/schema.go), checked-in
  [`schema/v1alpha1.schema.json`](../../schema/v1alpha1.schema.json) and
  [`schema/v1alpha2.schema.json`](../../schema/v1alpha2.schema.json), and the
  versioned references
  [`agentflow-v1alpha1.md`](../reference/agentflow-v1alpha1.md) and
  [`agentflow-v1alpha2.md`](../reference/agentflow-v1alpha2.md).
- **Conformance:**
  [`TestGeneratedSchemaArtifactsAreCurrent`](../../internal/workflow/schema_test.go),
  [`TestGeneratedSchemaDeclaresAuthorityAndRuntimeBoundary`](../../internal/workflow/schema_test.go),
  [`TestReferenceDocumentsPointToGeneratedSchemas`](../../internal/workflow/schema_test.go),
  and the conformance corpus check artifact drift, authority labels, and
  unsupported boundaries.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow -run '^(TestGeneratedSchema|TestReferenceDocumentsPointToGeneratedSchemas|TestConformance)' -count=1`
- **Recovery:** Not applicable to schema generation. The same authority
  boundary is recovery-safe because unsupported authored recovery input is
  rejected before engine construction; direct-engine protection is covered by
  [`TestNewRejectsNonExecutableActivePhaseRecovery`](../../internal/engine/runtime_test.go).

## Stage 1B — `v1alpha1` runtime parity

### 1B-E1 — Every documented executable construct is implemented or explicitly non-executable

Status: **proven**, with the non-executable surface explicitly fail-closed.

- **Artifacts:** the supported inventory and limits are documented in
  [`docs/reference/runtime.md`](../reference/runtime.md); runtime behavior is
  implemented across [`internal/engine`](../../internal/engine), and the
  schema/runtime boundary is generated by
  [`internal/workflow/schema.go`](../../internal/workflow/schema.go).
  Compatibility-only `allowed_semantic_changes` and authored
  `recovery.activePhase` are marked unsupported rather than treated as
  enforcement.
- **Conformance:**
  [`TestConformanceCorpus`](../../internal/workflow/conformance_test.go),
  [`TestValidateMarksDocumentedButUnimplementedPhaseKindsUnsupported`](../../internal/workflow/validate_test.go),
  [`TestGeneratedSchemaDeclaresAuthorityAndRuntimeBoundary`](../../internal/workflow/schema_test.go),
  and [`TestNewRejectsNonExecutableActivePhaseRecovery`](../../internal/engine/runtime_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow ./internal/agentflowcli ./internal/engine -run '^(TestConformance|TestGeneratedSchema|TestValidateMarksDocumentedButUnimplementedPhaseKindsUnsupported|TestNewRejectsNonExecutableActivePhaseRecovery)' -count=1`
- **Recovery:** `activePhase` cannot authorize recovery: validation rejects it
  before repository access, and the direct engine constructor rejects it before
  runtime recovery. Normal recovery is derived from durable state and lifecycle
  policy, as proven by 1B-E3.

### 1B-E2 — The shell-orchestrated reference workflow completes through AgentFlow without semantic loss

Status: **proven**.

- **Artifacts:** the checked-in reference workflow
  [`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml),
  runtime contract in [`docs/reference/runtime.md`](../reference/runtime.md),
  and the deterministic reference repository/provider fixture in
  [`internal/engine/reference_workflow_test.go`](../../internal/engine/reference_workflow_test.go).
- **Conformance:**
  [`TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle`](../../internal/engine/reference_workflow_test.go)
  runs the exact checked-in workflow with a deterministic provider and verifies
  actor phases, quality gate, progress, bookkeeping, conditional human-gate
  path, checkpoint/completion state, clean workspace, and a successful restart.
  The fixture disables the optional human acknowledgement so the run remains
  deterministic; interactive human-gate behavior is covered by the runtime
  human-gate tests in the 1B-E3 recovery/runtime evidence.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle$' -count=1`
- **Recovery:** The test constructs a new engine after completion and asserts
  that restarting does not replay actor-owned phases. The retained-work
  safe-resume seam is additionally covered by 1B-E3.

### 1B-E3 — Restarting at any durable phase boundary does not replay accepted work

Status: **proven**.

- **Artifacts:** ordered lifecycle/recovery contract in
  [`docs/reference/runtime.md`](../reference/runtime.md), durable runtime in
  [`internal/engine/runtime_phase.go`](../../internal/engine/runtime_phase.go),
  [`runtime_lifecycle.go`](../../internal/engine/runtime_lifecycle.go), and
  [`runtime_checkpoint.go`](../../internal/engine/runtime_checkpoint.go), with
  human-gate handling in
  [`runtime_human.go`](../../internal/engine/runtime_human.go).
- **Conformance:**
  [`durability_test.go`](../../internal/engine/durability_test.go) covers
  partial work, actor completion, repair budgets, safety-terminal state,
  lineage, and authoritative markers;
  [`runtime_phase_conformance_test.go`](../../internal/engine/runtime_phase_conformance_test.go)
  covers pending-invocation interruption windows; and
  [`runtime_human_test.go`](../../internal/engine/runtime_human_test.go) covers
  human-gate and completion-validation durability; and
  [`TestReferenceV1Alpha1SafeResumeRecoversRetainedWorkWithoutProceduralRecovery`](../../internal/engine/reference_workflow_test.go)
  proves reference safe resume without authored procedural recovery.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^(Test(InitializeRequiresCleanWorkspaceAndCapturesState|Resume|RuntimeOwnedLifecycle|ValidationRepairsOnceAndExhaustionSurvivesRestart|SafetyFailureIsDurable|RecoverActiveSafety|PhaseMarkerIsAuthoritative|ReferenceV1Alpha1SafeResume)|TestAdversarial)' -count=1`
  and the race-enabled `GOCACHE=/tmp/agentflow-foundation-gocache ./scripts/check.sh`.
- **Recovery:** This is the primary recovery criterion. Durable pending
  invocation evidence is reconciled before replay; `actor_completed` repeats
  acceptance without replay; valid completion markers win over stale active
  state; unauthorized movement becomes terminal safety state; partial commits
  and dirty work are preserved rather than deleted. The cited tests exercise
  these crash windows and verify idempotent restart outcomes.

## Stage 1C — Runtime-owned orchestration and concise SDL authoring

### 1C-E1 — Safe resume needs no procedural active-phase recovery

Status: **proven**.

- **Artifacts:** concise fixture
  [`concise-defaults.yaml`](../../internal/workflow/testdata/conformance/valid/concise-defaults.yaml),
  shipped example
  [`art-portfolio.agent-workflow.yaml`](../../examples/art-portfolio.agent-workflow.yaml),
  and safe-resume contract in
  [`docs/reference/runtime.md`](../reference/runtime.md).
- **Conformance:**
  [`TestNormalizeWorkflowResolvesConciseDefaults`](../../internal/workflow/normalize_test.go),
  [`TestBuildExpandedPlanExposesRuntimeContract`](../../internal/workflow/normalize_test.go),
  and [`TestReferenceV1Alpha1SafeResumeRecoversRetainedWorkWithoutProceduralRecovery`](../../internal/engine/reference_workflow_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow ./internal/engine -run '^(TestNormalizeWorkflowResolvesConciseDefaults|TestBuildExpandedPlanExposesRuntimeContract|TestReferenceV1Alpha1SafeResumeRecoversRetainedWorkWithoutProceduralRecovery)$' -count=1`
  and `go run . plan --expanded -f internal/workflow/testdata/conformance/valid/concise-defaults.yaml`.
- **Recovery:** The reference recovery test seeds active phase state with no
  `recovery.activePhase`, resumes from persisted state, and verifies exactly
  one actor invocation for the retained phase.

### 1C-E2 — Scope, protected integrity, lineage, and cleanliness are declared once and enforced at boundaries

Status: **proven**.

- **Artifacts:** normalized workspace authority in
  [`internal/workflow/normalize.go`](../../internal/workflow/normalize.go),
  centralized enforcement in
  [`internal/engine/runtime_policy.go`](../../internal/engine/runtime_policy.go),
  and the boundary contract in
  [`docs/reference/runtime.md`](../reference/runtime.md).
- **Conformance:**
  [`runtime_policy_test.go`](../../internal/engine/runtime_policy_test.go),
  [`TestMutationPolicyLineageIsEnforcedOnResume`](../../internal/engine/durability_test.go),
  [`TestInvalidatedMarkerAndLineageChangesDoNotAdvanceWork`](../../internal/engine/durability_test.go),
  and [`TestExpandedPlanIncludesCompleteNormalizedExecutionContract`](../../internal/workflow/plan_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine ./internal/workflow -run '^(Test(MutationPolicyLineageIsEnforcedOnResume|InvalidatedMarkerAndLineageChangesDoNotAdvanceWork)|TestExpandedPlanIncludesCompleteNormalizedExecutionContract|Test(Integrity|SafeRelativeIntegrity|AssertIntegrity|Checkpoint))' -count=1`
- **Recovery:** Resume rechecks the same scope, integrity, lineage, and
  cleanliness policies before accepting retained work or a completed marker;
  the lineage/marker tests verify that changed or invalidated state cannot
  advance work.

### 1C-E3 — Criterion acceptance is engine-owned

Status: **proven**.

- **Artifacts:** stable `criterionID` model and normalization in
  [`internal/workflow/model.go`](../../internal/workflow/model.go) and
  [`normalize.go`](../../internal/workflow/normalize.go); runtime ownership in
  [`internal/engine/runtime_lifecycle.go`](../../internal/engine/runtime_lifecycle.go)
  and actor boundary in
  [`internal/engine/actor_quarantine.go`](../../internal/engine/actor_quarantine.go).
- **Conformance:**
  [`TestEngineOwnedProgressAdvancesOnlyDeclaredCriterion`](../../internal/engine/markdown_bookkeeping_test.go),
  [`TestEngineOwnedProgressRejectsActorControlledOrExtraProgress`](../../internal/engine/markdown_bookkeeping_test.go),
  [`TestLoopDispatchUsesStableCriterionIDs`](../../internal/engine/control_flow_test.go),
  and the end-to-end reference test in 1B-E2.
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^(TestEngineOwnedProgress|TestLoopDispatchUsesStableCriterionIDs)$' -count=1`
- **Recovery:**
  [`TestEngineOwnedProgressRecoveryFinishesPendingTransition`](../../internal/engine/markdown_bookkeeping_test.go)
  verifies a pending engine-owned transition is completed on recovery without
  granting the actor control of progress.

### 1C-E4 — Deterministic completion bookkeeping does not consume an AI phase

Status: **proven**.

- **Artifacts:** constrained Markdown transition engine in
  [`internal/engine/markdown_bookkeeping.go`](../../internal/engine/markdown_bookkeeping.go)
  and actor-less bookkeeping phase in the checked-in
  [`spec/agent-workflow-v1alpha1.yaml`](../../spec/agent-workflow-v1alpha1.yaml).
- **Conformance:**
  [`TestMarkdownBookkeepingPreservesContentAndFailsClosed`](../../internal/engine/markdown_bookkeeping_test.go),
  [`TestBookkeepingRejectsMutationOutsideItsDeclaredBoundary`](../../internal/engine/markdown_bookkeeping_test.go),
  and the provider-call assertions in
  [`TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle`](../../internal/engine/reference_workflow_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^(TestMarkdownBookkeeping|TestBookkeeping|TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle)$' -count=1`
- **Recovery:**
  [`TestBookkeepingRecoveryFinishesDurablePendingTransition`](../../internal/engine/markdown_bookkeeping_test.go)
  and the engine-owned progress recovery test complete pending deterministic
  transitions without an AI/provider replay.

### 1C-E5 — Equivalent deterministic validation reuses evidence; changed dependencies rerun it

Status: **proven**.

- **Artifacts:** content-addressed evidence key and execution in
  [`internal/engine/validation_evidence.go`](../../internal/engine/validation_evidence.go)
  and [`runtime_phase.go`](../../internal/engine/runtime_phase.go); public
  contract in [`docs/reference/runtime.md`](../reference/runtime.md).
- **Conformance:**
  [`TestValidationEvidenceReusesOnlyEquivalentDeclaredState`](../../internal/engine/validation_evidence_test.go),
  [`TestValidationEvidenceSurvivesRestartButNotSafetyBoundary`](../../internal/engine/validation_evidence_test.go),
  [`TestValidationEvidenceIsWrittenOnlyAfterRepairRerunsTheGate`](../../internal/engine/validation_evidence_test.go),
  and canonical-gate coverage in
  [`TestReferenceV1Alpha1CanonicalGateEvidenceReusesAndInvalidates`](../../internal/engine/reference_workflow_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^(TestValidationEvidence|TestReferenceV1Alpha1CanonicalGateEvidenceReusesAndInvalidates)$' -count=1`
- **Recovery:** The restart test reuses durable evidence once, but executes
  safety checks before trusting it. The canonical reference test observes one
  gate invocation for identical state and a second invocation after a declared
  dependency changes; cached success cannot bypass a safety failure.

### 1C-E6 — Common agents, lifecycle, and repair policy use defaults/references

Status: **proven**.

- **Artifacts:** concise lowering in
  [`internal/workflow/authoring_syntax.go`](../../internal/workflow/authoring_syntax.go)
  and [`normalize.go`](../../internal/workflow/normalize.go), with authoring
  contract in [`docs/guides/concise-authoring.md`](../guides/concise-authoring.md).
- **Conformance:**
  [`TestAllowWritesExpandsToMutationPolicy`](../../internal/workflow/authoring_syntax_test.go),
  [`TestInlinePhaseActorExpandsToNamedAgentAndUsesDefaults`](../../internal/workflow/authoring_syntax_test.go),
  [`TestValidationRunPreservesRepairSemantics`](../../internal/workflow/authoring_syntax_test.go),
  [`TestNormalizeWorkflowResolvesConciseDefaults`](../../internal/workflow/normalize_test.go),
  and the v1alpha2 normalization assertions in
  [`TestV1Alpha2ConformanceExampleStrictlyDecodesAndNormalizesAgentCapabilities`](../../internal/workflow/conformance_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow -run '^(Test(AllowWritesExpandsToMutationPolicy|InlinePhaseActorExpandsToNamedAgentAndUsesDefaults|ValidationRunPreservesRepairSemantics|NormalizeWorkflowResolvesConciseDefaults|V1Alpha2ConformanceExampleStrictlyDecodesAndNormalizesAgentCapabilities))$' -count=1`
- **Recovery:** Defaults are materialized into the normalized execution
  contract before runtime state is created. Restart identity and evidence keys
  therefore use resolved authority, as covered by the durable restart tests in
  1B-E3 and 1C-E5.

### 1C-E7 — `plan --expanded` exposes generated execution behavior before execution

Status: **proven**.

- **Artifacts:** expanded plan projection in
  [`internal/workflow/plan.go`](../../internal/workflow/plan.go), CLI handling in
  [`internal/agentflowcli/main.go`](../../internal/agentflowcli/main.go), and
  normalized runtime contract in
  [`docs/reference/runtime.md`](../reference/runtime.md).
- **Conformance:**
  [`TestExpandedPlanIncludesCompleteNormalizedExecutionContract`](../../internal/workflow/plan_test.go),
  [`TestExpandedPlanMaterializesConciseDefaultsWithoutRetainingAuthoringDefaults`](../../internal/workflow/plan_test.go),
  [`TestExpandedPlanRevealsResolvedDefaultsAndAcceptanceOrder`](../../internal/workflow/plan_test.go),
  and CLI checks
  [`TestPlanExpandedCLI`](../../internal/agentflowcli/main_test.go) and
  [`TestPlanExpandedCLIExposesV1Alpha2DependencyGraph`](../../internal/agentflowcli/main_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow ./internal/agentflowcli -run '^(TestExpandedPlan|TestPlanExpandedCLI)' -count=1`
  and `go run . plan --expanded -f examples/feature.agent-workflow.yaml`.
- **Recovery:** Planning does not mutate or recover a workspace. Its relevance
  is that lifecycle, recovery, validation, checkpoint, progress, and
  completion defaults are visible before a run can create durable state.

### 1C-E8 — Representative authoring reads as domain policy and work intent

Status: **proven**.

- **Artifacts:** representative concise workflow
  [`v1alpha2-concise.yaml`](../../internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml),
  shipped [`feature.agent-workflow.yaml`](../../examples/feature.agent-workflow.yaml),
  and the authority-preserving migration guide
  [`migrating-v1alpha1-to-v1alpha2.md`](../guides/migrating-v1alpha1-to-v1alpha2.md).
- **Conformance:**
  [`TestV1Alpha2ConformanceExampleStrictlyDecodesAndNormalizesAgentCapabilities`](../../internal/workflow/conformance_test.go),
  [`TestV1Alpha2ConformanceExampleExecutesAuthorityBoundaries`](../../internal/engine/dependency_scheduler_test.go),
  and [`TestConciseAuthoringParityAcrossVersions`](../../internal/workflow/v1alpha2_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow ./internal/engine -run '^(Test(V1Alpha2ConformanceExample|ConciseAuthoringParityAcrossVersions))' -count=1`
- **Recovery:** The concise form omits procedural recovery and runtime-private
  plumbing; runtime-owned restart behavior is covered by the reference and
  durable recovery tests in 1B-E3. The migration guide requires retaining
  v1alpha1 when the concise version cannot express required authority.

### 1C-E9 — Actor, mutation, and deterministic-validation authority remain distinct

Status: **proven**.

- **Artifacts:** separate authority fields in
  [`internal/workflow/model.go`](../../internal/workflow/model.go) and
  [`internal/workflow/plan.go`](../../internal/workflow/plan.go), plus runtime
  enforcement in [`internal/engine/runtime_phase.go`](../../internal/engine/runtime_phase.go).
- **Conformance:**
  [`TestInvocationScopedMayCommitPrimaryAndRepairAuthorities`](../../internal/engine/invocation_may_commit_test.go),
  [`TestRunAgentEnforcesMayCommitAtEachActorInvocation`](../../internal/engine/runtime_phase_test.go),
  [`TestRunAgentV1Alpha2ProviderCapabilitiesMatchSharedAgent`](../../internal/engine/runtime_phase_test.go),
  and [`TestV1Alpha2CapabilitiesPreserveDurableRuntimeAuthority`](../../internal/engine/runtime_phase_test.go).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/engine -run '^(Test(InvocationScopedMayCommit|RunAgentEnforcesMayCommit|RunAgentV1Alpha2ProviderCapabilitiesMatchSharedAgent|V1Alpha2CapabilitiesPreserveDurableRuntimeAuthority))' -count=1`
- **Recovery:** Invocation authority is persisted and rechecked during pending
  invocation reconciliation and recovered actor execution; the pending and
  durability suites cited in 1B-E3 cover that boundary.

### 1C-E10 — Incompatible grammar changes use a new alpha version and migration path

Status: **proven**.

- **Artifacts:** explicit migration procedure in
  [`migrating-v1alpha1-to-v1alpha2.md`](../guides/migrating-v1alpha1-to-v1alpha2.md),
  separate v1alpha1/v1alpha2 references
  ([`v1alpha1`](../reference/agentflow-v1alpha1.md),
  [`v1alpha2`](../reference/agentflow-v1alpha2.md)), and checked-in
  versioned schemas.
- **Conformance:**
  [`TestDecodeKeepsV1Alpha1Behavior`](../../internal/workflow/v1alpha2_test.go),
  [`TestV1Alpha1RepairOnceCompatibilityRemainsBounded`](../../internal/workflow/v1alpha2_test.go),
  [`TestV1Alpha2ReferencesFailClosed`](../../internal/workflow/v1alpha2_test.go),
  and the v1alpha1/v1alpha2 boundary fixture
  [`v1alpha1-rejects-v1alpha2-dependency.yaml`](../../internal/workflow/testdata/conformance/invalid/v1alpha1-rejects-v1alpha2-dependency.yaml).
- **Command:**
  `GOCACHE=/tmp/agentflow-foundation-gocache go test ./internal/workflow -run '^(Test(DecodeKeepsV1Alpha1Behavior|V1Alpha1RepairOnceCompatibilityRemainsBounded|V1Alpha2ReferencesFailClosed|ConformanceInvalidDiagnostics))$' -count=1`
- **Recovery:** Version changes do not reinterpret persisted v1alpha1 state.
  The migration procedure requires expanded-plan comparison and retaining
  v1alpha1 when authority cannot be expressed; durable restart behavior remains
  governed by the v1alpha1 contract in 1B-E3.

## Implemented support versus rejected non-executable surface

The following distinction is part of the closure decision. A structurally
known field is not necessarily executable: unsupported input returns a stable
unsupported outcome and is rejected before an engine is created.

| Surface | Implemented support | Explicitly rejected or outside the executable surface | Evidence |
| --- | --- | --- | --- |
| Authoring and validation | Strict YAML decoding, typed/reference validation, source paths/positions, generated v1alpha1/v1alpha2 schemas, and positive/negative conformance | Unknown executable fields, malformed types/expressions, duplicate IDs, unknown references, and cross-version fields | [`load.go`](../../internal/workflow/load.go), [`validate.go`](../../internal/workflow/validate.go), [`schema.go`](../../internal/workflow/schema.go), [`conformance_test.go`](../../internal/workflow/conformance_test.go) |
| Runtime core | Typed parameters, bounded expressions, preconditions/assertions, allowed-path policy, integrity/lineage/cleanliness checks, named AI phases, deterministic validations, bounded repair, checkpoints, progress, human gates, completion, and safe resume | Arbitrary programming-language expressions and constructs not listed in the runtime inventory | [`docs/reference/runtime.md`](../reference/runtime.md), [`internal/engine`](../../internal/engine) |
| Runtime-owned lifecycle | Safe-resume lifecycle, pending invocation reconciliation, actor-completion evidence, deterministic acceptance, engine-owned progress/bookkeeping, completion markers, and content-addressed validation evidence | Authored `recovery.activePhase`; it is never executed and is rejected as unsupported | [`runtime_lifecycle.go`](../../internal/engine/runtime_lifecycle.go), [`validation_evidence.go`](../../internal/engine/validation_evidence.go), [`active-phase-recovery.yaml`](../../internal/workflow/testdata/conformance/unsupported/active-phase-recovery.yaml) |
| Provider/workspace boundary | Provider-neutral actor interface, built-in Codex boundary enforcement, workspace mutation authority, protected integrity, and actor progress quarantine | Built-in Codex approval policies other than `never`, non-Git workspaces, unsafe provider boundary claims, and unsupported actor capabilities | [`provider/provider.go`](../../provider/provider.go), [`runtime_phase_test.go`](../../internal/engine/runtime_phase_test.go), [`runtime-surface.yaml`](../../internal/workflow/testdata/conformance/unsupported/runtime-surface.yaml) |
| Tools and phases | Shell, workspace-policy, Git-checkpoint, file-regex, and Markdown-checklist tools; AI and bookkeeping phase kinds | `tool` and `human` phase kinds, unsupported tool types, and custom tool plugins in this stage | [`docs/reference/runtime.md`](../reference/runtime.md), [`validate_test.go`](../../internal/workflow/validate_test.go) |
| Compatibility-only declarations | Descriptive metadata/source fields remain distinguishable from executable `spec` | `allowed_semantic_changes` is not runtime enforcement and is rejected as unsupported; v1alpha2 does not silently claim full v1alpha1 schema parity | [`schema/v1alpha1.schema.json`](../../schema/v1alpha1.schema.json), [`allowed-semantic-changes.yaml`](../../internal/workflow/testdata/conformance/unsupported/allowed-semantic-changes.yaml), [`concise-authoring.md`](../guides/concise-authoring.md) |

Rejected surfaces are not hidden completion gaps: they are documented,
classified, and tested as fail-closed boundaries. A later stage may extend a
surface only through a versioned or explicitly compatible contract.

## Deliberately deferred work

These items remain later execution-stage work. They are recorded to prevent
this closeout from claiming them as complete.

- **Stage 2 — Runtime security and execution ownership:** the full stage
  includes one live workflow owner, stable-process stale-owner recovery,
  cancellation, resource/network/credential scopes, human approval for
  privileged effects, and complete model/tool/token/time/money budgets. Existing
  boundary tests support Stage 1's safety claims but do not close the full
  Stage 2 scope.
- **Stage 3 — Run identity, supervised sessions, and trace foundation:** stable
  run/node identities, explainable transitions, exclusive attach/detach
  handoff, and lossless operational output remain later work.
- **Stage 4 — `v1alpha1` maintenance and successor migration:** freezing
  v1alpha1 authoring, migrating the canonical self-hosting path, deprecation,
  and release migration support are not claimed here. Stage 1 only proves the
  explicit v1alpha1-to-v1alpha2 migration boundary.
- **Stage 5 — Typed contracts, artifacts, and evidence:** machine-checkable
  phase handoffs and acceptance artifacts beyond the current validation and
  runtime evidence are deferred.
- **Stage 6 — Parallel dependency scheduling:** the current v1alpha2 scheduler
  is deliberately serial. Bounded concurrency, fan-out/fan-in, conflict
  detection, and parallel recovery are not implemented by this closeout.
- **Stage 7 — Executor and tool extensibility:** custom provider/tool plugins
  remain outside the executable surface until they use the established
  capability, identity, and typed-contract boundaries.
- **Stage 8 — Reusable workflows and composition:** pinned, trust-aware
  composition is deferred.
- **Stage 9 — Developer tooling and observability completion:** semantic
  comparison, documentation drift tooling, metrics, and observability exports
  beyond the current checked-in behavior are deferred.
- **Stage 10 — v1beta1 stabilization:** semantic freeze, normative publication,
  supported migration and release paths, and v1beta1 conformance are deferred.

## Reproduction record

The required repository quality gate passed on this evidence baseline:

```text
GOCACHE=/tmp/agentflow-foundation-gocache ./scripts/check.sh
```

The gate covers formatting, diff hygiene, ordinary tests, vet, race-enabled
tests, CLI build, and validation of every checked-in executable definition in
`spec/` and `examples/`. The focused commands listed above provide the
criterion-level checks; the gate is the final aggregate check. No external
product documentation or network source is part of this record.
