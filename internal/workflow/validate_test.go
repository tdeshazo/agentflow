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
      validation: phaseGate
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

func TestValidateRejectsDefaultRuntimeLifecycleWithoutValidation(t *testing.T) {
	body := strings.Replace(executableFixture, "      validation: phaseGate\n", "", 1)
	result := ValidateFile(writeWorkflow(t, body))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Path == "spec.phases[0].validation" && strings.Contains(diagnostic.Message, "runtime-owned lifecycle") {
			return
		}
	}
	t.Fatalf("diagnostics = %#v", result.Diagnostics)
}

func TestValidateEngineOwnedProgressAndBookkeeping(t *testing.T) {
	valid := `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: engine-owned-transitions
spec:
  agents:
    worker:
      runner: codex
  tools:
    scope:
      type: workspace-policy
  validation:
    gate:
      steps:
        - uses: scope
  lifecycle:
    policy: safe-resume
    validation: gate
  progress:
    criteria:
      - id: criterion-one
        text: One stable criterion
  phases:
    - id: one
      kind: criterion
      label: one
      actor: worker
      criterionID: criterion-one
      advanceProgress: true
      prompt: implement one
    - id: close
      kind: bookkeeping
      label: close
      bookkeeping:
        - type: markdown-status
          path: roadmap.md
          label: Status
          from: In Progress
          to: Complete
  flow:
    - phase: one
    - phase: close
`
	if result := ValidateFile(writeWorkflow(t, valid)); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	invalid := strings.Replace(valid, "criterionID: criterion-one\n      advanceProgress", "criterionID: missing\n      advanceProgress", 1)
	result := ValidateFile(writeWorkflow(t, invalid))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Path == "spec.phases[0].criterionID" && strings.Contains(diagnostic.Message, "unknown criterion id") {
			return
		}
	}
	t.Fatalf("diagnostics = %#v", result.Diagnostics)
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

func TestValidateReportsDecoderErrorsAtYAMLPath(t *testing.T) {
	tests := []struct {
		name    string
		replace string
		with    string
		path    string
		message string
	}{
		{
			name:    "unknown field",
			replace: "label: build",
			with:    "label: build\n      unknowable: true",
			path:    "spec.phases[0].unknowable",
			message: "field unknowable not found",
		},
		{
			name:    "malformed type",
			replace: "prompt: make the bounded change",
			with:    "requiresChange: definitely\n      prompt: make the bounded change",
			path:    "spec.phases[0].requiresChange",
			message: "cannot unmarshal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(executableFixture, tt.replace, tt.with, 1)))
			if result.Status != Invalid || len(result.Diagnostics) != 1 {
				t.Fatalf("result = %#v", result)
			}
			diagnostic := result.Diagnostics[0]
			if diagnostic.Path != tt.path || diagnostic.Position.Line == 0 || diagnostic.Position.Column == 0 || !strings.Contains(diagnostic.Message, tt.message) {
				t.Fatalf("diagnostic = %#v, want path %q, source position, and %q", diagnostic, tt.path, tt.message)
			}
		})
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

func TestValidateRejectsHumanAndCompletionFailuresBeforeRuntime(t *testing.T) {
	body := strings.Replace(executableFixture, "  flow:", `  humanGates:
    - id: approval
      requires: [build]
      acknowledgement: {type: free-form, value: ''}
      checklist: [{id: review, text: ''}, {id: review, text: repeated}]
      evidence: {record: approved, value: actor_controlled}
  completion:
    done:
      writeMarker: {record: complete, value: actor_controlled}
      summary:
        include: [mystery, final_gate_green, mystery]
  flow:`, 1)
	body = strings.Replace(body, "    - phase: build", "    - phase: build\n    - human: approval\n    - complete: done", 1)
	result := ValidateFile(writeWorkflow(t, body))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	want := []struct{ path, message string }{
		{"spec.humanGates[0].acknowledgement.type", "must be exact-text"},
		{"spec.humanGates[0].acknowledgement.value", "is required"},
		{"spec.humanGates[0].checklist[0].text", "is required"},
		{"spec.humanGates[0].checklist[1].id", "duplicate checklist item id"},
		{"spec.humanGates[0].evidence.value", "only head_commit is executable"},
		{"spec.completion.done.writeMarker.value", "only head_commit is executable"},
		{"spec.completion.done.summary.include[0]", "unsupported completion summary field"},
		{"spec.completion.done.summary.include[1]", "requires finalValidation"},
		{"spec.completion.done.summary.include[2]", "duplicate completion summary field"},
	}
	for _, expected := range want {
		if !diagnosticsContain(result.Diagnostics, expected.path, expected.message) {
			t.Errorf("missing diagnostic at %s containing %q: %#v", expected.path, expected.message, result.Diagnostics)
		}
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

func TestValidatePreconditionScope(t *testing.T) {
	valid := strings.Replace(executableFixture, "  agents:", `  preconditions:
    - id: initial-file
      scope: initialization
      type: files-exist
      paths: [README.md]
  agents:`, 1)
	if result := ValidateFile(writeWorkflow(t, valid)); result.Status != Executable {
		t.Fatalf("initialization scope status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	invalid := strings.Replace(valid, "scope: initialization", "scope: retry-only", 1)
	result := ValidateFile(writeWorkflow(t, invalid))
	if result.Status != Invalid {
		t.Fatalf("invalid scope status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	if !diagnosticsContain(result.Diagnostics, "spec.preconditions[0].scope", "unsupported precondition scope") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestValidateAssertionToolsByType(t *testing.T) {
	valid := strings.Replace(executableFixture, "  flow:\n    - phase: build", `  completion:
    default:
      assertions:
        - uses: scope
  flow:
    - phase: build`, 1)
	if result := ValidateFile(writeWorkflow(t, valid)); result.Status != Executable {
		t.Fatalf("named workspace-policy assertion status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	unsafe := strings.Replace(valid, "        - uses: scope\n  flow:", "        - uses: checkpoint\n  flow:", 1)
	unsafe = strings.Replace(unsafe, "    scope:\n      type: workspace-policy", "    scope:\n      type: workspace-policy\n    checkpoint:\n      type: git-checkpoint", 1)
	result := ValidateFile(writeWorkflow(t, unsafe))
	if result.Status != Invalid {
		t.Fatalf("mutating assertion status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	if !diagnosticsContain(result.Diagnostics, "spec.completion.default.assertions[0].uses", "not supported in assertion context") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func diagnosticsContain(diagnostics []Diagnostic, path, message string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Path == path && strings.Contains(diagnostic.Message, message) {
			return true
		}
	}
	return false
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
		filepath.Join("..", "..", "spec", "agent-workflow.yaml"),
		filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"),
	} {
		r := ValidateFile(path)
		if r.Status != Executable {
			t.Fatalf("%s status = %s, want executable: %#v", path, r.Status, r.Diagnostics)
		}
	}
}
