package workflow

import (
	"strings"
	"testing"
)

func TestV1Alpha2RejectsEscapingWorkspacePaths(t *testing.T) {
	const fixture = `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: workspace-paths}
spec:
  workspace:
    allowWrites: [src/**]
    integrity:
      - id: protected
        paths: [docs/**]
        exclude: [docs/generated/**]
        mode: exact-hash
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
  validation:
    quality:
      run: go test ./...
      dependencies: [src/**]
  phases:
    - {id: implement, actor: coder, prompt: Implement., validation: quality}
  completion: {validation: quality}
`
	tests := []struct {
		name    string
		replace string
		with    string
		path    string
	}{
		{
			name:    "allowWrites",
			replace: "allowWrites: [src/**]",
			with:    "allowWrites: [src/../../outside]",
			path:    "spec.workspace.allowWrites[0]",
		},
		{
			name:    "validation dependency",
			replace: "dependencies: [src/**]",
			with:    "dependencies: [src/../../outside]",
			path:    "spec.validation.quality.dependencies[0]",
		},
		{
			name:    "integrity path",
			replace: "paths: [docs/**]",
			with:    "paths: [docs/../../outside]",
			path:    "spec.workspace.integrity[0].paths[0]",
		},
		{
			name:    "integrity exclusion",
			replace: "exclude: [docs/generated/**]",
			with:    `exclude: [docs\..\..\outside]`,
			path:    "spec.workspace.integrity[0].exclude[0]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(fixture, test.replace, test.with, 1)))
			if result.Status != Invalid || !diagnosticsContain(result.Diagnostics, test.path, "must be workspace-relative") {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
		})
	}
}

func TestSuccessorContractsRejectEscapingWorkspacePaths(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		replace string
		with    string
		path    string
	}{
		{
			name:    "v1alpha3 artifact",
			fixture: v1alpha3Fixture,
			replace: "paths: [src/result.txt]",
			with:    "paths: [src/../../outside]",
			path:    "spec.artifacts.implementation-result.paths[0]",
		},
		{
			name:    "v1alpha4 Markdown adapter",
			fixture: v1alpha4Fixture,
			replace: "path: progress.md",
			with:    `path: src\..\..\outside.md`,
			path:    "spec.criteria.markdownChecklist.path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(test.fixture, test.replace, test.with, 1)))
			if result.Status != Invalid || !diagnosticsContain(result.Diagnostics, test.path, "must be workspace-relative") {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
		})
	}
}
