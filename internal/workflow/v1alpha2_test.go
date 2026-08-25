package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const v1alpha2Fixture = `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata:
  name: v1alpha2-fixture
spec:
  workspace:
    allowWrites: [src/**, tests/**]
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
    reviewer: {runner: codex, model: gpt-5.6-luna}
  validation:
    tests:
      run: go test ./...
      repair: {once: coder}
  phases:
    - {id: implement, actor: coder, prompt: Implement the feature., validation: tests}
    - {id: review, actor: reviewer, prompt: Review the feature., validation: tests, dependsOn: [implement]}
  completion: {validation: tests}
`

func TestDecodeDispatchesV1Alpha2AndNormalizesDependencies(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1alpha2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if d.V1Alpha2 == nil || d.Workflow == nil || d.Workflow.APIVersion != v1alpha2APIVersion {
		t.Fatalf("decoded document = %#v", d)
	}
	if got := d.Workflow.Spec.Workspace.MutationPolicy.Allowed; len(got) != 2 || got[0] != "src/**" || got[1] != "tests/**" {
		t.Fatalf("normalized allowlist = %#v", got)
	}
	if got := d.PhaseDependencies["review"]; len(got) != 1 || got[0] != "implement" {
		t.Fatalf("dependencies = %#v", d.PhaseDependencies)
	}
	result := Validate(d)
	if result.Status != Executable || result.Normalized == nil {
		t.Fatalf("status = %s, normalized = %#v, diagnostics = %#v", result.Status, result.Normalized, result.Diagnostics)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Phases) != 2 || len(plan.Phases[1].DependsOn) != 1 || plan.Phases[1].DependsOn[0] != "implement" {
		t.Fatalf("planned phases = %#v", plan.Phases)
	}
}

func TestV1Alpha2RepairNormalizesToBoundedDeterministicValidation(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1alpha2Fixture))
	if err != nil {
		t.Fatal(err)
	}

	v := d.Workflow.Spec.Validation["tests"]
	if v.OnFailure.Strategy != "repair-once" {
		t.Fatalf("repair strategy = %q, want repair-once", v.OnFailure.Strategy)
	}
	if v.OnFailure.MaxRepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want exactly one", v.OnFailure.MaxRepairAttempts)
	}
	if v.OnFailure.Repair.Actor != "coder" {
		t.Fatalf("repair actor = %q, want coder", v.OnFailure.Repair.Actor)
	}
	if len(v.Steps) != 1 || v.Steps[0].Uses != v1Alpha2ValidationToolName("tests") {
		t.Fatalf("deterministic validation steps = %#v", v.Steps)
	}
	if len(v.OnFailure.Then) != 0 {
		t.Fatalf("unexpected alternate post-repair steps = %#v", v.OnFailure.Then)
	}
}

func TestV1Alpha2ConformanceExampleRemainsStrictlyDecoded(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(body), "      run: make test", "      run: make test\n      unknowable: true", 1)
	_, err = Decode(writeWorkflow(t, unknown))
	if err == nil || !strings.Contains(err.Error(), "field unknowable not found") {
		t.Fatalf("strict decode error = %v", err)
	}
}

func TestDecodeKeepsV1Alpha1Behavior(t *testing.T) {
	d, err := Decode(writeWorkflow(t, executableFixture))
	if err != nil {
		t.Fatal(err)
	}
	if d.V1Alpha2 != nil || d.Workflow.APIVersion != v1alpha1APIVersion {
		t.Fatalf("decoded document = %#v", d)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	_, err = Decode(writeWorkflow(t, strings.Replace(executableFixture, "prompt: make the bounded change", "prompt: make the bounded change\n      dependsOn: [other]", 1)))
	if err == nil || !strings.Contains(err.Error(), "field dependsOn not found") {
		t.Fatalf("v1alpha1 dependsOn error = %v", err)
	}
}

func TestDecodeV1Alpha2KeepsKnownFieldsStrict(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture, "runner: codex, model: gpt-5.6-terra", "runner: codex, model: gpt-5.6-terra, teleport: true", 1)
	_, err := Decode(writeWorkflow(t, document))
	if err == nil || !strings.Contains(err.Error(), "field teleport not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeRejectsUnknownAPIVersion(t *testing.T) {
	_, err := Decode(writeWorkflow(t, strings.Replace(v1alpha2Fixture, v1alpha2APIVersion, "agentflow.dev/v9", 1)))
	if err == nil || !strings.Contains(err.Error(), `unsupported apiVersion "agentflow.dev/v9"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestV1Alpha2ReferencesFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		replace  string
		with     string
		path     string
		contains string
	}{
		{
			name:     "unknown phase dependency",
			replace:  "dependsOn: [implement]",
			with:     "dependsOn: [missing]",
			path:     "spec.phases[1].dependsOn[0]",
			contains: "unknown phase dependency",
		},
		{
			name:     "unknown repair actor",
			replace:  "repair: {once: coder}",
			with:     "repair: {once: missing}",
			path:     "spec.validation.tests.repair.once",
			contains: "unknown agent",
		},
		{
			name:     "dependency cycle",
			replace:  "- {id: implement, actor: coder, prompt: Implement the feature., validation: tests}",
			with:     "- {id: implement, actor: coder, prompt: Implement the feature., validation: tests, dependsOn: [review]}",
			path:     "spec.phases[0].dependsOn",
			contains: "dependency cycle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			document := strings.Replace(v1alpha2Fixture, tc.replace, tc.with, 1)
			result := ValidateFile(writeWorkflow(t, document))
			if result.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Path == tc.path && diagnostic.Position.Line > 0 && strings.Contains(diagnostic.Message, tc.contains) {
					return
				}
			}
			t.Fatalf("diagnostics = %#v", result.Diagnostics)
		})
	}
}

func TestV1Alpha2RepairPolicyFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		replace string
		with    string
		want    string
	}{
		{
			name:    "missing actor",
			replace: "repair: {once: coder}",
			with:    "repair: {}",
			want:    "repair.once is required",
		},
		{
			name:    "empty actor",
			replace: "repair: {once: coder}",
			with:    "repair: {once: ''}",
			want:    "repair.once is required",
		},
		{
			name:    "unknown actor",
			replace: "repair: {once: coder}",
			with:    "repair: {once: missing}",
			want:    "unknown agent",
		},
		{
			name:    "malformed scalar policy",
			replace: "repair: {once: coder}",
			with:    "repair: once",
			want:    "repair must be a mapping",
		},
		{
			name:    "malformed expanded policy",
			replace: "repair: {once: coder}",
			with:    "repair: {strategy: repair-once, maxRepairAttempts: 1, actor: coder}",
			want:    "field strategy not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(v1alpha2Fixture, tc.replace, tc.with, 1)))
			if result.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if strings.Contains(diagnostic.Message, tc.want) {
					return
				}
			}
			t.Fatalf("diagnostics = %#v, want %q", result.Diagnostics, tc.want)
		})
	}
}

func TestV1Alpha2RepairAliasesStillRequireDeclaredActor(t *testing.T) {
	body := strings.Replace(v1alpha2Fixture, "model: gpt-5.6-terra", "model: &repair_actor missing", 1)
	body = strings.Replace(body, "repair: {once: coder}", "repair: {once: *repair_actor}", 1)
	result := ValidateFile(writeWorkflow(t, body))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Path == "spec.validation.tests.repair.once" && strings.Contains(diagnostic.Message, "unknown agent") {
			return
		}
	}
	t.Fatalf("diagnostics = %#v", result.Diagnostics)
}

func TestV1Alpha2RepairMergeCannotHideAuthority(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: merged-repair-policy}
spec:
  workspace: {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation:
    tests:
      run: make test
      repair:
        <<: {once: coder}
  phases: [{id: implement, actor: coder, prompt: implement, validation: tests}]
  completion: {validation: tests}
`))
	if err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported in spec.validation.tests.repair") {
		t.Fatalf("merge error = %v", err)
	}
}

func TestV1Alpha1RepairOnceCompatibilityRemainsBounded(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	n, err := NormalizeWorkflow(d)
	if err != nil {
		t.Fatal(err)
	}
	v := n.Workflow.Spec.Validation["gate"]
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts != 1 || v.OnFailure.Repair.Actor != "worker" {
		t.Fatalf("v1alpha1 repair contract changed: %#v", v.OnFailure)
	}
}

func TestV1Alpha2PreservesScalarsAndRejectsMergeKeys(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: scalar-values}
spec:
  workspace: {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation:
    tests:
      run: >-
        go test ./...
        && go vet ./...
  phases:
    - id: implement
      actor: coder
      prompt: |-
        Implement the feature.

        Keep the public API stable.
      validation: tests
  completion: {validation: tests}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.V1Alpha2.Spec.Validation["tests"].Run, "go test ./... && go vet ./..."; got != want {
		t.Fatalf("folded run = %q, want %q", got, want)
	}
	if got, want := d.Workflow.Spec.Phases[0].Prompt, "Implement the feature.\n\nKeep the public API stable."; got != want {
		t.Fatalf("literal prompt = %q, want %q", got, want)
	}

	_, err = Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: merged-authority}
spec:
  workspace:
    <<: &policy {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation: {tests: {run: "true"}}
  phases: [{id: implement, actor: coder, prompt: implement, validation: tests}]
  completion: {validation: tests}
`))
	if err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported in spec.workspace") {
		t.Fatalf("merge error = %v", err)
	}
}
