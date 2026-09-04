package workflow

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConformanceCorpus(t *testing.T) {
	cases := []struct {
		name   string
		status Status
	}{
		{name: "valid/minimal.yaml", status: Executable},
		{name: "valid/concise-defaults.yaml", status: Executable},
		{name: "valid/v1alpha2-concise.yaml", status: Executable},
		{name: "valid/v1alpha3-typed-contracts.yaml", status: Executable},
		{name: "valid/v1alpha4-typed-work-items.yaml", status: Executable},
		{name: "valid/v1alpha2-provider-requirements.yaml", status: Executable},
		{name: "valid/v1alpha2-execution-policy.yaml", status: Executable},
		{name: "valid/v1alpha2-tool-config.yaml", status: Executable},
		{name: "invalid/v1alpha1-provider-requirements.yaml", status: Invalid},
		{name: "invalid/v1alpha1-tool-config.yaml", status: Invalid},
		{name: "unsupported/runtime-surface.yaml", status: Executable},
		{name: "unsupported/allowed-semantic-changes.yaml", status: Unsupported},
		{name: "unsupported/active-phase-recovery.yaml", status: Unsupported},
		{name: "unsupported/v1alpha2-approval-policy.yaml", status: Unsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
		})
	}
}

func TestV1Alpha2ConformanceExampleStrictlyDecodesAndNormalizesAgentCapabilities(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml")
	d, err := Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	result := Validate(d)
	if result.Status != Executable || result.Normalized == nil {
		t.Fatalf("status = %s, normalized = %#v, diagnostics = %#v", result.Status, result.Normalized, result.Diagnostics)
	}

	normalized := result.Normalized.Workflow
	if got, want := normalized.Spec.Workspace.MutationPolicy.Allowed, []string{"src/**", "tests/**"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalized workspace authority = %#v, want %#v", got, want)
	}
	wantCoder := Agent{
		Runner: "codex", Model: "gpt-5.6-terra", Sandbox: "workspace-write", Approval: "never",
		Ephemeral: true, MayCommit: true, OutputLastMessage: true,
	}
	if got := normalized.Spec.Agents["coder"]; !reflect.DeepEqual(got, wantCoder) {
		t.Fatalf("coder authority = %#v, want %#v", got, wantCoder)
	}
	wantReview := Agent{
		Runner: "codex", Model: "gpt-5.6-luna", Sandbox: "workspace-write", Approval: "never",
		Ephemeral: true, MayCommit: false, OutputLastMessage: true,
	}
	if got := normalized.Spec.Agents["__inline_actor__review"]; !reflect.DeepEqual(got, wantReview) {
		t.Fatalf("inline review authority = %#v, want %#v", got, wantReview)
	}

	validation := normalized.Spec.Validation["tests"]
	if len(validation.Steps) != 1 || normalized.Spec.Tools[validation.Steps[0].Uses].Command != "make test" {
		t.Fatalf("normalized validation = %#v, tools = %#v", validation, normalized.Spec.Tools)
	}
	if validation.OnFailure.Strategy != "repair-once" || validation.OnFailure.MaxRepairAttempts != 1 || validation.OnFailure.Repair.Actor != "coder" || len(validation.OnFailure.Then) != 0 {
		t.Fatalf("normalized repair policy = %#v", validation.OnFailure)
	}

	graph := normalized.DependencyGraph
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("normalized dependency graph = %#v", graph)
	}
	edge := graph.Edges[0]
	if edge.Phase != "review" || edge.DependsOn != "implement" || edge.SatisfiedWhen != PhaseDependencyAccepted {
		t.Fatalf("dependency edge = %#v", edge)
	}

	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := plan.WorkspaceMutationAllowlist, []string{"src/**", "tests/**"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expanded workspace authority = %#v, want %#v", got, want)
	}
	if got := []string{plan.ResolvedAgents[0].Name, plan.ResolvedAgents[1].Name}; strings.Join(got, ",") != "__inline_actor__review,coder" {
		t.Fatalf("resolved actor order = %#v", got)
	}
	if len(plan.Phases) != 2 || plan.Phases[1].ID != "review" || len(plan.Phases[1].DependsOn) != 1 || plan.Phases[1].DependsOn[0] != "implement" {
		t.Fatalf("expanded phases = %#v", plan.Phases)
	}
	if !strings.Contains(strings.Join(plan.Phases[0].Acceptance, "|"), "deterministic validation") || !strings.Contains(strings.Join(plan.CompletionContract, "|"), "final validation tests") {
		t.Fatalf("expanded acceptance contract = %#v", plan)
	}

	first, err := yaml.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		repeated, err := yaml.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		if string(repeated) != string(first) {
			t.Fatalf("expanded plan changed on run %d", i)
		}
	}
}

func TestConformanceInvalidDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		status   Status
		contains string
	}{
		{name: "unknown-executable-field.yaml", status: Invalid, contains: "field unknowable not found"},
		{name: "malformed-type.yaml", status: Invalid, contains: "cannot unmarshal"},
		{name: "duplicate-identifiers.yaml", path: "spec.preconditions[1].id", status: Invalid, contains: "duplicate check id"},
		{name: "unknown-references.yaml", path: "spec.phases[0].actor", status: Invalid, contains: "unknown agent"},
		{name: "invalid-expression.yaml", path: "spec.workspace.root", status: Invalid, contains: "unknown parameter reference"},
		{name: "malformed-expression.yaml", path: "spec.state.reset.when", status: Invalid, contains: "missing closing delimiter"},
		{name: "v1alpha1-rejects-v1alpha2-dependency.yaml", status: Invalid, contains: "field dependsOn not found"},
		{name: "v1alpha2-unknown-field.yaml", status: Invalid, contains: "field unknown not found"},
		{name: "unsafe-identifiers.yaml", path: "spec.agents", status: Invalid, contains: "agent name must not be empty"},
		{name: "v1alpha2-policy-escalation.yaml", status: Invalid, contains: "broadens inherited network"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", "invalid", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if (tc.path == "" || diagnostic.Path == tc.path) &&
					diagnostic.Status == tc.status && strings.Contains(diagnostic.Message, tc.contains) {
					return
				}
			}
			t.Fatalf("no diagnostic with path %q and message containing %q: %#v", tc.path, tc.contains, result.Diagnostics)
		})
	}
}

func TestConformanceUnknownReferencesCoverEveryAuthorityDomain(t *testing.T) {
	result := ValidateFile(filepath.Join("testdata", "conformance", "invalid", "unknown-references.yaml"))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	want := []struct {
		path    string
		message string
	}{
		{path: "spec.validation.phase-gate.steps[0].uses", message: "unknown tool"},
		{path: "spec.phases[0].actor", message: "unknown agent"},
		{path: "spec.phases[0].criterionID", message: "unknown criterion id"},
		{path: "spec.humanGates[0].requires[0]", message: "unknown phase"},
		{path: "spec.flow[0].phase", message: "unknown phase"},
		{path: "spec.flow[0].validate", message: "unknown validation"},
		{path: "spec.flow[1].human", message: "unknown human gate"},
		{path: "spec.flow[2].complete", message: "unknown completion"},
	}
	for _, expected := range want {
		if !diagnosticsContain(result.Diagnostics, expected.path, expected.message) {
			t.Errorf("missing %s diagnostic containing %q: %#v", expected.path, expected.message, result.Diagnostics)
		}
	}
}

func TestConformanceUnsafeIdentifiersCoverDurableNames(t *testing.T) {
	result := ValidateFile(filepath.Join("testdata", "conformance", "invalid", "unsafe-identifiers.yaml"))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	want := []struct {
		path    string
		message string
	}{
		{path: "spec.parameters", message: "parameter name must not be empty"},
		{path: "spec.paths", message: "path name must not be empty"},
		{path: "spec.agents", message: "agent name must not be empty"},
		{path: "spec.tools", message: "tool name must not be empty"},
		{path: "spec.validation", message: "validation name must not be empty"},
		{path: "spec.completion", message: "completion name must not be empty"},
		{path: "spec.state.records.integrity", message: "integrity record name must not be empty"},
		{path: "spec.phases[0].id", message: "must match"},
		{path: "spec.humanGates[0].id", message: "must match"},
		{path: "spec.flow[0].id", message: "must match"},
	}
	for _, expected := range want {
		if !diagnosticsContain(result.Diagnostics, expected.path, expected.message) {
			t.Errorf("missing %s diagnostic containing %q: %#v", expected.path, expected.message, result.Diagnostics)
		}
	}
}

func TestConformanceDiagnosticOrderIsStable(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "invalid", "unknown-references.yaml")
	first := ValidateFile(path)
	for i := 0; i < 20; i++ {
		result := ValidateFile(path)
		if len(result.Diagnostics) != len(first.Diagnostics) {
			t.Fatalf("run %d diagnostics = %d, want %d", i, len(result.Diagnostics), len(first.Diagnostics))
		}
		for j, diagnostic := range result.Diagnostics {
			want := first.Diagnostics[j]
			if diagnostic.Status != want.Status || diagnostic.Path != want.Path || diagnostic.Position != want.Position || diagnostic.Message != want.Message {
				t.Fatalf("run %d diagnostic %d changed:\n got %#v\nwant %#v", i, j, diagnostic, want)
			}
		}
	}
}

func TestConformanceShippedDefinitions(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "spec", "agent-workflow.yaml"),
		filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"),
		filepath.Join("..", "..", "examples", "art-portfolio.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "art-portfolio-v1alpha1.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "feature.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "representative", "simple-implementation.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "representative", "implementation-independent-audit.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "representative", "human-gated-release.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "representative", "human-gated-release-v1alpha1.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "representative", "criterion-driven-multi-item.agent-workflow.yaml"),
	}
	for _, path := range paths {
		result := ValidateFile(path)
		if result.Status != Executable {
			t.Fatalf("%s status = %s, want executable: %#v", path, result.Status, result.Diagnostics)
		}
	}
}
