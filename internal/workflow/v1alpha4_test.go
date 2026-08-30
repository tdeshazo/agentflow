package workflow

import (
	"strings"
	"testing"
)

const v1alpha4Fixture = `
apiVersion: agentflow.dev/v1alpha4
kind: AgentWorkflow
metadata: {name: typed-work-items}
spec:
  workspace:
    allowWrites: [src/**, progress.md]
  agents:
    implementer: {runner: codex, model: gpt-5.6-terra, may_commit: false}
  validation:
    quality: {run: test -f src/result.txt}
  criteria:
    items:
      - {id: add-api, description: Add the API capability.}
      - {id: add-tests, description: Add the regression tests.}
    markdownChecklist:
      path: progress.md
      items: {add-api: Add the API capability., add-tests: Add the regression tests.}
  phases:
    - id: implement-items
      kind: implementation
      actor: implementer
      prompt: Complete only the assigned work item.
      validation: quality
      advanceWorkItem: true
      forEach: {workItems: [add-api, add-tests], maxItems: 2}
  completion: {validation: quality}
`

func TestV1Alpha4LowersBoundedWorkItemCollection(t *testing.T) {
	document, err := Decode(writeWorkflow(t, v1alpha4Fixture))
	if err != nil {
		t.Fatal(err)
	}
	result := Validate(document)
	if result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	w := result.Normalized.Workflow
	if w.APIVersion != v1alpha4APIVersion || len(w.Spec.Criteria.Items) != 2 {
		t.Fatalf("normalized criteria = %#v", w)
	}
	if got := []string{w.Spec.Phases[0].ID, w.Spec.Phases[1].ID}; strings.Join(got, ",") != "implement-items--add-api,implement-items--add-tests" {
		t.Fatalf("expanded phases = %#v", w.Spec.Phases)
	}
	for _, phase := range w.Spec.Phases {
		if !phase.AdvanceWorkItem || phase.WorkItemID == "" || !strings.Contains(phase.Prompt, "Assigned work item") {
			t.Fatalf("expanded work item phase = %#v", phase)
		}
	}
	plan, err := BuildExpandedPlan(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Criteria.Items) != 2 || len(plan.Phases) != 2 || plan.Phases[0].WorkItemID != "add-api" {
		t.Fatalf("expanded plan = %#v", plan)
	}
}

func TestV1Alpha4RejectsUnboundedOrAmbiguousWorkItemAdvancement(t *testing.T) {
	tests := []struct {
		name     string
		replace  string
		with     string
		contains string
	}{
		{
			name:     "collection bound must equal static target count",
			replace:  "maxItems: 2",
			with:     "maxItems: 3",
			contains: "statically bounded",
		},
		{
			name:     "every item has one exact target",
			replace:  "workItems: [add-api, add-tests]",
			with:     "workItems: [add-api]",
			contains: "has no exact-target advancement",
		},
		{
			name:     "adapter maps every declared item",
			replace:  "add-tests: Add the regression tests.",
			with:     "",
			contains: "must map work item",
		},
		{
			name:     "advancement cannot be skipped by a condition",
			replace:  "validation: quality\n      advanceWorkItem",
			with:     "validation: quality\n      if: false\n      advanceWorkItem",
			contains: "cannot be conditional",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(v1alpha4Fixture, test.replace, test.with, 1)))
			if result.Status != Invalid || !diagnosticsMessageContains(result.Diagnostics, test.contains) {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
		})
	}
}
