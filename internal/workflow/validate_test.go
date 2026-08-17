package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const executableFixture = `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: validate-fixture
spec:
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
      prompt: make the bounded change
  flow:
    - phase: build
`

func writeWorkflow(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateExecutableWithoutRepository(t *testing.T) {
	r := ValidateFile(writeWorkflow(t, executableFixture))
	if r.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
}

func TestValidateRuntimeOwnedLifecyclePolicy(t *testing.T) {
	valid := strings.Replace(executableFixture, "  phases:", `  lifecycle:
    policy: safe-resume
    validation: phaseGate
  phases:`, 1)
	if result := ValidateFile(writeWorkflow(t, valid)); result.Status != Executable {
		t.Fatalf("compact lifecycle status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	invalid := strings.Replace(valid, "policy: safe-resume", "policy: unsafe", 1)
	result := ValidateFile(writeWorkflow(t, invalid))
	if result.Status != Invalid {
		t.Fatalf("invalid lifecycle status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Path == "spec.lifecycle.policy" && strings.Contains(diagnostic.Message, "unsupported lifecycle policy") {
			return
		}
	}
	t.Fatalf("lifecycle diagnostics = %#v", result.Diagnostics)
}

func TestValidateReportsCrossReferenceAtYAMLPath(t *testing.T) {
	body := strings.Replace(executableFixture, "actor: worker", "actor: missing", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s", r.Status)
	}
	if len(r.Diagnostics) == 0 || r.Diagnostics[0].Path != "spec.phases[0].actor" {
		t.Fatalf("diagnostic = %#v", r.Diagnostics)
	}
	if r.Diagnostics[0].Position.Line == 0 {
		t.Fatalf("missing source position: %#v", r.Diagnostics[0])
	}
}

func TestValidateRejectsUnknownExecutableField(t *testing.T) {
	body := strings.Replace(executableFixture, "label: build", "label: build\n      unknowable: true", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s", r.Status)
	}
	if !strings.Contains(r.Diagnostics[0].Message, "field unknowable not found") {
		t.Fatalf("diagnostic = %#v", r.Diagnostics)
	}
}

func TestValidateRejectsWrongStructuralType(t *testing.T) {
	body := strings.Replace(executableFixture, "prompt: make the bounded change", "requiresChange: definitely\n      prompt: make the bounded change", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s", r.Status)
	}
	if !strings.Contains(r.Diagnostics[0].Message, "cannot unmarshal") {
		t.Fatalf("diagnostic = %#v", r.Diagnostics)
	}
}

func TestValidateAcceptsImplementedRuntimeSurface(t *testing.T) {
	body := strings.Replace(executableFixture, "type: workspace-policy", "type: file-regex", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
}

func TestValidateMarksDocumentedButUnimplementedPhaseKindsUnsupported(t *testing.T) {
	body := strings.Replace(executableFixture, "kind: implementation", "kind: human", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Unsupported {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
}

func TestValidateRejectsUnknownPreconditionsBeforeRuntime(t *testing.T) {
	body := strings.Replace(executableFixture, "  agents:", `  preconditions:
    - id: unknown-check
      type: mutable-magic
  agents:`, 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
	if len(r.Diagnostics) == 0 || r.Diagnostics[0].Path != "spec.preconditions[0].type" {
		t.Fatalf("diagnostics = %#v", r.Diagnostics)
	}
}

func TestValidateRejectsInvalidControlFlowExpressions(t *testing.T) {
	cases := []struct {
		name    string
		replace string
		want    string
	}{
		{name: "unknown condition reference", replace: "if: \"{{ parameters.missing }}\"\n    - phase: build", want: "unknown parameter reference"},
		{name: "unsupported function", replace: "if: \"{{ evaluate('anything') }}\"\n    - phase: build", want: "unsupported expression function"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(executableFixture, "  flow:\n    - phase: build", "  flow:\n    - "+tc.replace, 1)
			r := ValidateFile(writeWorkflow(t, body))
			if r.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
			}
			found := false
			for _, diagnostic := range r.Diagnostics {
				if strings.Contains(diagnostic.Message, tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("diagnostics = %#v, want %q", r.Diagnostics, tc.want)
			}
		})
	}
}

func TestValidateRejectsUnknownStateReferenceBeforeExecution(t *testing.T) {
	body := strings.Replace(executableFixture, "prompt: make the bounded change", "prompt: \"{{ state.not_a_runtime_value }}\"", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
	if len(r.Diagnostics) == 0 || !strings.Contains(r.Diagnostics[0].Message, "unknown state reference") {
		t.Fatalf("diagnostics = %#v", r.Diagnostics)
	}
}

func TestValidateRejectsUnknownIntegrityMode(t *testing.T) {
	body := strings.Replace(executableFixture, "  agents:", `  workspace:
    mutationPolicy:
      integrity:
        - id: protected
          paths: [README.md]
          mode: content-maybe
  agents:`, 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
	}
	found := false
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Path == "spec.workspace.mutationPolicy.integrity[0].mode" && strings.Contains(diagnostic.Message, "unknown integrity mode") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %#v", r.Diagnostics)
	}
}

func TestReferenceDocumentsAreSpecValid(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"),
		filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml"),
	} {
		r := ValidateFile(path)
		if r.Status == Invalid {
			t.Fatalf("%s invalid: %#v", path, r.Diagnostics)
		}
	}
}
