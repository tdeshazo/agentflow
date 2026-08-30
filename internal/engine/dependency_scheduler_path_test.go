package engine

import (
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestV1Alpha2ScheduleContractRejectsEscapingWorkspacePaths(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*workflow.Workflow)
		contains string
	}{
		{
			name: "allowWrites",
			mutate: func(w *workflow.Workflow) {
				w.Spec.Workspace.MutationPolicy.Allowed = []string{"src/../../outside"}
			},
			contains: "allowWrites[0] must be workspace-relative",
		},
		{
			name: "validation dependency",
			mutate: func(w *workflow.Workflow) {
				validation := w.Spec.Validation["gate"]
				validation.Dependencies = []string{`src\..\..\outside`}
				w.Spec.Validation["gate"] = validation
			},
			contains: `validation "gate" dependency 0 must be workspace-relative`,
		},
		{
			name: "artifact",
			mutate: func(w *workflow.Workflow) {
				w.Spec.Contracts.Artifacts = map[string]workflow.Artifact{
					"result": {Type: "files", Paths: []string{"src/../../outside"}, Persistence: "workspace"},
				}
			},
			contains: `artifact "result" path 0 must be workspace-relative`,
		},
		{
			name: "integrity exclusion",
			mutate: func(w *workflow.Workflow) {
				w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{
					{ID: "protected", Paths: []string{"docs/**"}, Exclude: []string{"docs/../../outside"}, Mode: "exact-hash"},
				}
			},
			contains: `integrity rule "protected" exclusion 0 must be workspace-relative`,
		},
		{
			name: "Markdown adapter",
			mutate: func(w *workflow.Workflow) {
				w.Spec.Criteria.MarkdownAdapter = &workflow.MarkdownChecklistAdapter{Path: "src/../../outside"}
			},
			contains: "Markdown checklist adapter path must be workspace-relative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := schedulingWorkflow(t.TempDir(), "unsafe-path", []string{"root"}, nil, "true")
			test.mutate(w)
			err := validateV1Alpha2ScheduleContract(w)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", err, test.contains)
			}
		})
	}
}
