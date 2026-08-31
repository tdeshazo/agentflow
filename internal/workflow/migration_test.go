package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV1Alpha1MigrationMatrixIsCompleteForCanonicalWorkflow(t *testing.T) {
	matrix, err := V1Alpha1MigrationMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Status != "supported-maintenance-frozen" {
		t.Fatalf("matrix status = %q", matrix.Status)
	}

	report, err := MigrationCheckFile(filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("migration report has no diagnostics")
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Note == "matrix coverage missing" {
			t.Fatalf("unclassified supported field: %#v", diagnostic)
		}
	}
}

func TestV1Alpha1MigrationMatrixClassifiesEverySchemaField(t *testing.T) {
	matrix, err := V1Alpha1MigrationMatrix()
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0)
	collectWorkflowFieldPaths(reflect.TypeFor[Workflow](), "", &paths, map[reflect.Type]bool{})
	// These strict concise spellings are part of v1alpha1's authored surface
	// even though AST lowering stores their expanded form in Workflow.
	paths = append(paths, "spec.workspace.allowWrites[]", "spec.validation.*.run")
	for _, path := range paths {
		if _, ok := migrationCapabilityFor(path, matrix); !ok {
			t.Errorf("matrix does not classify supported field %s", path)
		}
	}
}

func collectWorkflowFieldPaths(t reflect.Type, path string, paths *[]string, stack map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if stack[t] {
			return
		}
		stack[t] = true
		defer delete(stack, t)
		for i := range t.NumField() {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if !field.IsExported() || tag == "" || tag == "-" {
				continue
			}
			child := tag
			if path != "" {
				child = path + "." + tag
			}
			collectWorkflowFieldPaths(field.Type, child, paths, stack)
		}
	case reflect.Map:
		collectWorkflowFieldPaths(t.Elem(), path+".*", paths, stack)
	case reflect.Array, reflect.Slice:
		collectWorkflowFieldPaths(t.Elem(), path+"[]", paths, stack)
	default:
		*paths = append(*paths, path)
	}
}

func TestMigrationCheckClassifiesRepresentativesWithoutRewriting(t *testing.T) {
	path := writeWorkflow(t, executableFixture)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := MigrationCheckFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("migration check rewrote its source")
	}
	got := map[string]MigrationClass{}
	for _, diagnostic := range report.Diagnostics {
		got[diagnostic.Path] = diagnostic.Classification
	}
	for path, want := range map[string]MigrationClass{
		"spec.agents.worker.runner": DirectSuccessorCapability,
		"spec.tools.scope.type":     DirectSuccessorCapability,
		"spec.phases[0].prompt":     DirectSuccessorCapability,
		"spec.flow[0].phase":        GeneralizedReplacement,
	} {
		if got[path] != want {
			t.Errorf("%s classification = %q, want %q", path, got[path], want)
		}
	}
}

func TestMigrationCheckPointsStableProgressToV1Alpha4(t *testing.T) {
	report, err := MigrationCheckFile(filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	diagnostics := make(map[string]MigrationDiagnostic, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		diagnostics[diagnostic.Path] = diagnostic
	}
	const successor = "agentflow.dev/v1alpha4 spec.criteria, phase workItem/advanceWorkItem, and optional markdownChecklist adapter"
	for _, path := range []string{
		"spec.progress.source.path",
		"spec.progress.criteria[0].id",
		"spec.progress.criterionPhaseInvariant.targeted_item_must_be_checked",
		"spec.phases[0].criterionID",
		"spec.phases[0].advanceProgress",
	} {
		diagnostic, ok := diagnostics[path]
		if !ok {
			t.Errorf("missing migration diagnostic for %s", path)
			continue
		}
		if diagnostic.Classification != GeneralizedReplacement {
			t.Errorf("%s classification = %q, want %q", path, diagnostic.Classification, GeneralizedReplacement)
		}
		if diagnostic.Successor != successor {
			t.Errorf("%s successor = %q, want %q", path, diagnostic.Successor, successor)
		}
		if !strings.Contains(diagnostic.Note, "typed work items") || !strings.Contains(diagnostic.Note, "does not replace runtime rediscovery loops") {
			t.Errorf("%s note does not distinguish stable progress from dynamic loops: %q", path, diagnostic.Note)
		}
	}
}

func TestMigrationCheckKeepsDynamicProgressRediscoveryInV1Alpha1(t *testing.T) {
	path := writeWorkflow(t, `apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: dynamic-progress-migration}
spec:
  parameters:
    maxSteps: {type: integer, default: 1}
  agents: {worker: {runner: codex}}
  tools: {gate: {type: shell, command: "true"}}
  validation: {gate: {steps: [{uses: gate}]}}
  progress:
    selection: {strategy: first-unchecked}
    criteria:
      - {id: one, text: First criterion}
  phases:
    - {id: one, kind: criterion, actor: worker, criterionID: one, advanceProgress: true, validation: gate, prompt: implement}
  flow:
    - loop:
        while: "{{ progress.unchecked_count > 0 }}"
        maxIterations: "{{ parameters.maxSteps }}"
        select: "{{ progress.next_unchecked }}"
        dispatchByCriterion: {one: one}
        requireUncheckedCountDelta: -1
`)
	report, err := MigrationCheckFile(path)
	if err != nil {
		t.Fatal(err)
	}

	diagnostics := make(map[string]MigrationDiagnostic, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		diagnostics[diagnostic.Path] = diagnostic
	}
	for _, path := range []string{
		"spec.progress.selection.strategy",
		"spec.flow[0].loop.while",
		"spec.flow[0].loop.maxIterations",
		"spec.flow[0].loop.select",
		"spec.flow[0].loop.dispatchByCriterion.one",
		"spec.flow[0].loop.requireUncheckedCountDelta",
	} {
		diagnostic, ok := diagnostics[path]
		if !ok {
			t.Errorf("missing migration diagnostic for %s", path)
			continue
		}
		if diagnostic.Classification != CompatibilityOnly || diagnostic.Successor != "none" {
			t.Errorf("%s migration = %#v, want compatibility-only with no successor", path, diagnostic)
		}
		if !strings.Contains(diagnostic.Note, "runtime") || !strings.Contains(diagnostic.Note, "v1alpha4") {
			t.Errorf("%s note does not explain the v1alpha4 boundary: %q", path, diagnostic.Note)
		}
	}
}

func TestMigrationCheckClassifiesAuthoredEmptyCollections(t *testing.T) {
	path := writeWorkflow(t, `apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: empty-migration-collections
spec:
  parameters: {}
  preconditions: []
  agents:
    worker:
      runner: codex
      approval: never
  tools:
    scope:
      type: workspace-policy
  validation:
    phaseGate:
      steps:
        - uses: scope
  phases:
    - id: build
      kind: implementation
      label: build
      actor: worker
      validation: phaseGate
      prompt: make the bounded change
  flow:
    - phase: build
`)
	report, err := MigrationCheckFile(path)
	if err != nil {
		t.Fatal(err)
	}

	counts := make(map[string]int, len(report.Diagnostics))
	diagnostics := make(map[string]MigrationDiagnostic, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		counts[diagnostic.Path]++
		diagnostics[diagnostic.Path] = diagnostic
	}
	for _, want := range []struct {
		path string
		line int
	}{
		{path: "spec.parameters", line: 6},
		{path: "spec.preconditions", line: 7},
	} {
		if counts[want.path] != 1 {
			t.Errorf("%s diagnostic count = %d, want 1", want.path, counts[want.path])
			continue
		}
		diagnostic := diagnostics[want.path]
		if diagnostic.Line != want.line || diagnostic.Column != 3 {
			t.Errorf("%s position = %d:%d, want %d:3", want.path, diagnostic.Line, diagnostic.Column, want.line)
		}
		if diagnostic.Classification != DirectSuccessorCapability {
			t.Errorf("%s classification = %q, want %q", want.path, diagnostic.Classification, DirectSuccessorCapability)
		}
	}
	for _, path := range []string{"spec.agents", "spec.agents.worker", "spec.tools", "spec.validation", "spec.phases", "spec.flow"} {
		if counts[path] != 0 {
			t.Errorf("nonempty container %s diagnostic count = %d, want 0", path, counts[path])
		}
	}
}

func TestMigrationCheckRejectsNonV1Alpha1Source(t *testing.T) {
	_, err := MigrationCheckFile(filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml"))
	if err == nil || !strings.Contains(err.Error(), "requires an agentflow.dev/v1alpha1") {
		t.Fatalf("error = %v", err)
	}
}

func TestPhaseThreeMigrationsPreservePortableAuthority(t *testing.T) {
	t.Run("art portfolio successor preserves and strengthens the executable contract", func(t *testing.T) {
		legacy := phaseThreePlan(t, filepath.Join("..", "..", "examples", "art-portfolio-v1alpha1.agent-workflow.yaml"))
		successor := phaseThreePlan(t, filepath.Join("..", "..", "examples", "art-portfolio.agent-workflow.yaml"))
		legacySpec, successorSpec := legacy.NormalizedExecution.Spec, successor.NormalizedExecution.Spec

		if got, want := strings.Join(successor.WorkspaceMutationAllowlist, ","), strings.Join(legacy.WorkspaceMutationAllowlist, ","); got != want {
			t.Fatalf("mutation authority = %q, want %q", got, want)
		}
		for _, name := range []string{"builder", "reviewer"} {
			if got, want := successorSpec.Agents[name], legacySpec.Agents[name]; !reflect.DeepEqual(got, want) {
				t.Fatalf("agent %q = %#v, want %#v", name, got, want)
			}
		}
		for i, legacyPhase := range legacySpec.Phases {
			successorPhase := successorSpec.Phases[i]
			if successorPhase.ID != legacyPhase.ID || successorPhase.Kind != legacyPhase.Kind || successorPhase.Actor != legacyPhase.Actor || successorPhase.Reasoning != legacyPhase.Reasoning || successorPhase.RequiresChange != legacyPhase.RequiresChange || successorPhase.Validation != legacyPhase.Validation {
				t.Fatalf("phase %d successor = %#v, want portable authority from %#v", i, successorPhase, legacyPhase)
			}
		}
		for _, name := range []string{"backend-files", "frontend-files", "portfolio-files"} {
			if got := successorSpec.Validation[name].OnFailure; got.Strategy != "repair-once" || got.MaxRepairAttempts != 1 || got.Repair.Actor != "builder" {
				t.Fatalf("validation %q repair authority = %#v", name, got)
			}
		}
		if successorSpec.Completion["default"].FinalValidation != legacySpec.Completion["portfolio"].FinalValidation {
			t.Fatalf("final validation = %q, want %q", successorSpec.Completion["default"].FinalValidation, legacySpec.Completion["portfolio"].FinalValidation)
		}
		if !successorSpec.State.Initialize.RequireCleanWorkspace || !successorSpec.State.Initialize.RequireNamedBranch || !successorSpec.State.Lineage.RequireBaseIsAncestorOfHead || !successorSpec.State.Resume.RequireSameBranch {
			t.Fatalf("successor resume policy is not at least as strict: %#v", successorSpec.State)
		}
	})

	t.Run("human-gated release successor preserves durable human evidence", func(t *testing.T) {
		legacy := phaseThreePlan(t, filepath.Join("..", "..", "examples", "representative", "human-gated-release-v1alpha1.agent-workflow.yaml"))
		successor := phaseThreePlan(t, filepath.Join("..", "..", "examples", "representative", "human-gated-release.agent-workflow.yaml"))
		legacySpec, successorSpec := legacy.NormalizedExecution.Spec, successor.NormalizedExecution.Spec
		legacyGate, successorGate := legacySpec.HumanGates[0], successorSpec.HumanGates[0]
		if legacyGate.ID != successorGate.ID || !reflect.DeepEqual(legacyGate.Requires, successorGate.Requires) || !reflect.DeepEqual(legacyGate.Checklist, successorGate.Checklist) || legacyGate.Acknowledgement != successorGate.Acknowledgement || successorGate.Evidence != (Marker{Value: "head_commit"}) {
			t.Fatalf("human gate authority changed:\nlegacy %#v\nsuccessor %#v", legacyGate, successorGate)
		}
		if legacyGate.Evidence.Record == "" || successorGate.Evidence.Record != "" || successorGate.IdempotentRecord != "" {
			t.Fatalf("successor human evidence record must be runtime-owned:\nlegacy %#v\nsuccessor %#v", legacyGate, successorGate)
		}
		if successorSpec.Phases[0].ID != legacySpec.Phases[0].ID || successorSpec.Phases[0].Validation != legacySpec.Phases[0].Validation {
			t.Fatalf("release phase authority changed: %#v", successorSpec.Phases[0])
		}
		if successorSpec.Completion["default"].FinalValidation != "release-check" || !successorSpec.State.Initialize.RequireCleanWorkspace {
			t.Fatalf("successor completion or initialization authority is missing: %#v", successorSpec)
		}
	})

	t.Run("canonical self hosting successor preserves or strengthens portable authority", func(t *testing.T) {
		legacy := phaseThreePlan(t, filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"))
		successor := phaseThreePlan(t, filepath.Join("..", "..", "spec", "agent-workflow.yaml"))
		legacySpec, successorSpec := legacy.NormalizedExecution.Spec, successor.NormalizedExecution.Spec
		actorWrites := append([]string(nil), successor.WorkspaceMutationAllowlist...)
		if got := actorWrites[len(actorWrites)-1]; got != "spec/agent-workflow-progress.md" {
			t.Fatalf("runtime-owned adapter path = %q", got)
		}
		actorWrites = actorWrites[:len(actorWrites)-1]
		if got, want := strings.Join(actorWrites, ","), strings.Join(legacy.WorkspaceMutationAllowlist, ","); got != want {
			t.Fatalf("self-hosting actor mutation authority = %q, want %q", got, want)
		}
		wantIntegrity := map[string][]string{
			"repository-instructions":         {"CONTRIBUTING.md", "GO_STYLE_GUIDE.md", "CODE_REVIEW.md"},
			"canonical-self-hosting-workflow": {"spec/agent-workflow.yaml"},
			"v1alpha1-compatibility-workflow": {"spec/agent-workflow-v1alpha1.yaml"},
			"agentflow-authoring-contract":    {"skills/agentflow-spec/SKILL.md"},
			"canonical-quality-gate":          {"scripts/check.sh", ".github/workflows/quality.yml"},
			"canonical-roadmap":               {"ROADMAP.md"},
			"planning-guidance":               {"docs/planning/README.md"},
		}
		gotIntegrity := make(map[string][]string, len(successorSpec.Workspace.MutationPolicy.Integrity))
		root := filepath.Join("..", "..")
		for _, rule := range successorSpec.Workspace.MutationPolicy.Integrity {
			if rule.Mode != "exact-hash" {
				t.Errorf("self-hosting integrity rule %q mode = %q, want exact-hash", rule.ID, rule.Mode)
			}
			gotIntegrity[rule.ID] = rule.Paths
			for _, path := range rule.Paths {
				if _, err := os.Stat(filepath.Join(root, path)); err != nil {
					t.Errorf("self-hosting integrity rule %q references unusable path %q: %v", rule.ID, path, err)
				}
			}
		}
		if !reflect.DeepEqual(gotIntegrity, wantIntegrity) {
			t.Errorf("self-hosting integrity authority = %#v, want %#v", gotIntegrity, wantIntegrity)
		}
		for _, precondition := range successorSpec.Preconditions {
			if precondition.Type != "files-exist" {
				continue
			}
			for _, path := range precondition.Paths {
				if _, err := os.Stat(filepath.Join(root, path)); err != nil {
					t.Errorf("self-hosting required file %q is unusable: %v", path, err)
				}
			}
		}
		for _, phase := range successorSpec.Phases {
			if phase.Reasoning != "high" {
				t.Errorf("self-hosting phase %q reasoning = %q, want supported Codex effort high", phase.ID, phase.Reasoning)
			}
		}
		for _, name := range []string{"implementation-quality", "verification-quality"} {
			if got := successorSpec.Validation[name].OnFailure; got.Strategy != "repair-once" || got.MaxRepairAttempts != 1 || got.Repair.Actor != "implementer" {
				t.Fatalf("self-hosting validation %q repair authority = %#v", name, got)
			}
		}
		for _, name := range []string{"baseline-quality", "integrated-audit-quality", "final-audit-quality", "final-quality"} {
			if got := successorSpec.Validation[name].OnFailure; got.Strategy != "" || got.MaxRepairAttempts != 0 {
				t.Fatalf("hard self-hosting validation %q acquired repair authority: %#v", name, got)
			}
		}
		if len(successorSpec.HumanGates) != 1 || successorSpec.HumanGates[0].Acknowledgement.Type != "exact-text" || !reflect.DeepEqual(successorSpec.HumanGates[0].Checklist, legacySpec.HumanGates[0].Checklist) || successorSpec.Completion["default"].FinalValidation != "final-quality" {
			t.Fatalf("self-hosting human/completion authority = %#v / %#v", successorSpec.HumanGates, successorSpec.Completion)
		}
		if len(legacySpec.Progress.Criteria) != 2 || len(successorSpec.Criteria.Items) != 2 || successorSpec.Criteria.MarkdownAdapter == nil {
			t.Fatalf("typed criterion replacement is incomplete: legacy %#v successor %#v", legacySpec.Progress, successorSpec.Criteria)
		}
		if successorSpec.Criteria.MarkdownAdapter.Path != "spec/agent-workflow-progress.md" {
			t.Fatalf("Markdown adapter = %#v", successorSpec.Criteria.MarkdownAdapter)
		}
		phases := map[string]Phase{}
		for _, phase := range successorSpec.Phases {
			phases[phase.ID] = phase
		}
		if !phases["baseline-audit"].ReadOnly || phases["baseline-audit"].Validation != "baseline-quality" {
			t.Fatalf("baseline authority = %#v", phases["baseline-audit"])
		}
		for _, id := range []string{"implement-agentflow-change", "verify-agentflow-change"} {
			if !phases[id].AdvanceWorkItem || phases[id].WorkItemID != id || !phases[id].RequiresChange {
				t.Fatalf("work-item phase %q = %#v", id, phases[id])
			}
		}
		for _, id := range []string{"integrated-regression-audit", "final-implementation-audit"} {
			if !phases[id].ReadOnly || phases[id].Actor != "reviewer" || phases[id].RequiresChange {
				t.Fatalf("independent audit phase %q = %#v", id, phases[id])
			}
		}
		if successorSpec.Agents["reviewer"].MayCommit {
			t.Fatalf("reviewer acquired commit authority: %#v", successorSpec.Agents["reviewer"])
		}
		completion := successorSpec.Completion["default"]
		if !reflect.DeepEqual(completion.Evidence, []string{"integrated-audit-accepted", "final-audit-accepted"}) || len(completion.Assertions) != 2 {
			t.Fatalf("typed completion authority = %#v", completion)
		}
		if successorSpec.Lifecycle.Policy != "safe-resume" || !successorSpec.State.Initialize.RequireCleanWorkspace || !successorSpec.State.Initialize.RequireNamedBranch || !successorSpec.State.Lineage.RequireBaseIsAncestorOfHead || !successorSpec.State.Resume.RequireSameBranch {
			t.Fatalf("successor resume authority is not at least as strict: lifecycle=%#v state=%#v", successorSpec.Lifecycle, successorSpec.State)
		}
	})
}

func phaseThreePlan(t *testing.T, path string) ExpandedPlan {
	t.Helper()
	result := ValidateFile(path)
	if result.Status != Executable || result.Normalized == nil {
		t.Fatalf("%s status = %s, diagnostics = %#v", path, result.Status, result.Diagnostics)
	}
	plan, err := BuildExpandedPlan(result.Document)
	if err != nil {
		t.Fatalf("build expanded plan for %s: %v", path, err)
	}
	return plan
}
