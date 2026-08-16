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

func TestValidateDistinguishesUnsupportedRuntimeSurface(t *testing.T) {
	body := strings.Replace(executableFixture, "type: workspace-policy", "type: file-regex", 1)
	r := ValidateFile(writeWorkflow(t, body))
	if r.Status != Unsupported {
		t.Fatalf("status = %s, diagnostics = %#v", r.Status, r.Diagnostics)
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
