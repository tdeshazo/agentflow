package workflow

import (
	"strings"
	"testing"
)

const v1alpha3Fixture = `
apiVersion: agentflow.dev/v1alpha3
kind: AgentWorkflow
metadata: {name: typed-contracts}
spec:
  workspace:
    allowWrites: [src/**]
  agents:
    implementer: {runner: codex, model: gpt-5.6-terra, may_commit: true}
    auditor: {runner: codex, model: gpt-5.6-luna, may_commit: false}
  validation:
    implementation-quality:
      run: test -f src/result.txt
      produces: [implementation-accepted]
    audit-quality:
      run: test -f src/result.txt
      hard: true
      produces: [audit-accepted]
  artifacts:
    implementation-result:
      type: files
      paths: [src/result.txt]
      persistence: workspace
  evidence:
    implementation-accepted: {type: validation}
    audit-accepted: {type: validation}
  phases:
    - id: implement
      kind: implementation
      actor: implementer
      prompt: Implement the result.
      validation: implementation-quality
      outputs: [implementation-result]
    - id: audit
      kind: audit
      actor: auditor
      prompt: Audit the typed result without editing it.
      validation: audit-quality
      dependsOn: [implement]
      readOnly: true
      inputs:
        - artifact: implementation-result
        - evidence: implementation-accepted
      ifEvidence: implementation-accepted
  completion:
    validation: audit-quality
    evidence: [audit-accepted]
`

func TestV1Alpha3TypedContractsNormalizeWithoutChangingV1Alpha2(t *testing.T) {
	document, err := Decode(writeWorkflow(t, v1alpha3Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if document.V1Alpha3 == nil || document.V1Alpha2 != nil {
		t.Fatalf("authored version dispatch = %#v", document)
	}
	result := Validate(document)
	if result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	workflow := result.Normalized.Workflow
	if workflow.APIVersion != v1alpha3APIVersion || workflow.Spec.Phases[1].Inputs[0].Artifact != "implementation-result" || !workflow.Spec.Phases[1].ReadOnly {
		t.Fatalf("normalized typed contracts = %#v", workflow)
	}
	if got := workflow.Spec.Validation["implementation-quality"].ProducesEvidence; len(got) != 1 || got[0] != "implementation-accepted" {
		t.Fatalf("validation evidence = %#v", got)
	}
}

func TestV1Alpha3ContractsRejectMissingProducerAndUnsafeAudit(t *testing.T) {
	tests := []struct {
		name     string
		replace  string
		with     string
		contains string
	}{
		{
			name:     "input artifact requires producer dependency",
			replace:  "dependsOn: [implement]",
			with:     "dependsOn: []",
			contains: "requires dependsOn",
		},
		{
			name:     "audit requires read only declaration",
			replace:  "readOnly: true",
			with:     "readOnly: false",
			contains: "audit phases must declare readOnly",
		},
		{
			name:     "artifact needs exactly one producer",
			replace:  "outputs: [implementation-result]",
			with:     "outputs: [missing-result]",
			contains: "unknown artifact",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(v1alpha3Fixture, test.replace, test.with, 1)))
			if result.Status != Invalid || !diagnosticsMessageContains(result.Diagnostics, test.contains) {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
		})
	}
}

func diagnosticsMessageContains(diagnostics []Diagnostic, message string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, message) {
			return true
		}
	}
	return false
}
