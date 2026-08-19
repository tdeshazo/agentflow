package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestSelectWorkflowRendersSortedEntriesAndReturnsSelectedPath(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".agent-workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agent-workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	alphaPath := filepath.Join(repo, ".agent-workflows", "alpha.yaml")
	zetaPath := filepath.Join(repo, ".agent-workflows", "zeta.yaml")
	betaPath := filepath.Join(home, ".agent-workflows", "beta.yaml")
	writeCLIWorkflow(t, alphaPath, "alpha")
	writeCLIWorkflow(t, zetaPath, "zeta")
	writeCLIWorkflow(t, betaPath, "beta")
	var output bytes.Buffer
	path, err := pickWorkflow(repo, strings.NewReader("2\n"), &output, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if path != betaPath {
		t.Fatalf("selected path = %q", path)
	}
	if got, want := output.String(), "Select a workflow:\n1. alpha (repository)\n2. beta (global)\n3. zeta (repository)\nEnter a number: "; got != want {
		t.Fatalf("picker output = %q, want %q", got, want)
	}
}

func TestPickWorkflowUsesLocalPrecedenceAndReturnsResolvedPath(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	localDir := filepath.Join(repo, ".agent-workflows")
	globalDir := filepath.Join(home, ".agent-workflows")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(localDir, "shared.yaml")
	globalPath := filepath.Join(globalDir, "shared.yaml")
	writeCLIWorkflow(t, localPath, "local")
	writeCLIWorkflow(t, globalPath, "global")

	path, err := pickWorkflow(repo, strings.NewReader("1\n"), io.Discard, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if path != localPath {
		t.Fatalf("selected path = %q, want local path %q", path, localPath)
	}
}

func TestSelectWorkflowNoWorkflows(t *testing.T) {
	var output bytes.Buffer
	_, err := pickWorkflow(t.TempDir(), strings.NewReader("1\n"), &output, func() (string, error) { return t.TempDir(), nil })
	if err == nil || !strings.Contains(err.Error(), ".agent-workflows/") {
		t.Fatalf("no-workflow error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("no-workflow output = %q", output.String())
	}
}

func TestSelectWorkflowRejectsInvalidInputWithoutReprompting(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "\n", want: `invalid workflow selection ""`},
		{name: "text", input: "one\n", want: `invalid workflow selection "one"`},
		{name: "out of range", input: "3\n", want: "out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			discovery := workflow.Discovery{Files: []workflow.DiscoveryFile{{Name: "only", Path: "/only.yaml", Source: "repository"}}}
			var output bytes.Buffer
			_, err := selectWorkflow(discovery, strings.NewReader(tt.input), clioutput.NewPresenterWithMode(&output, true, false))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("selection error = %v, want %q", err, tt.want)
			}
			if strings.Count(output.String(), "Enter a number:") != 1 {
				t.Fatalf("picker reprompted: %q", output.String())
			}
		})
	}
}

func TestSelectWorkflowNOColorStillSelectsOnTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var output bytes.Buffer
	discovery := workflow.Discovery{Files: []workflow.DiscoveryFile{{Name: "only", Path: "/only.yaml", Source: "repository"}}}
	path, err := selectWorkflow(discovery, strings.NewReader("1\n"), clioutput.NewPresenterWithTTY(&output, true))
	if err != nil {
		t.Fatal(err)
	}
	if path != "/only.yaml" {
		t.Fatalf("selected path = %q", path)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("NO_COLOR picker output contains ANSI escapes: %q", output.String())
	}
}

func TestMissingWorkflowSelectorNonTTYDoesNotReadInput(t *testing.T) {
	originalDirectory := currentWorkingDirectory
	t.Cleanup(func() { currentWorkingDirectory = originalDirectory })
	currentWorkingDirectory = func() (string, error) { return t.TempDir(), nil }
	read := &panicReader{}
	var output bytes.Buffer
	err := runArgsWithIO([]string{"validate"}, read, &output)
	if err == nil || !strings.Contains(err.Error(), "-f workflow YAML is required") ||
		!strings.Contains(err.Error(), "workflow-name") || !strings.Contains(err.Error(), "-f workflow.yaml") {
		t.Fatalf("missing-selector error = %v", err)
	}
}

func TestInteractiveSelectionPassesResolvedPathToValidation(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	workflowDir := filepath.Join(repo, ".agent-workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	selectedPath := filepath.Join(workflowDir, "selected.yaml")
	writeCLIWorkflow(t, selectedPath, "selected")

	originalInteractive := workflowPickerInteractive
	originalHome := workflowHomeDirectory
	t.Cleanup(func() {
		workflowPickerInteractive = originalInteractive
		workflowHomeDirectory = originalHome
	})
	workflowPickerInteractive = func(io.Reader, io.Writer) bool { return true }
	workflowHomeDirectory = func() (string, error) { return home, nil }

	var output bytes.Buffer
	if err := runArgsWithIO([]string{"validate", "-C", repo}, strings.NewReader("1\n"), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "valid and executable") {
		t.Fatalf("validation output = %q", output.String())
	}
}

type panicReader struct{}

func (*panicReader) Read([]byte) (int, error) {
	panic("non-TTY workflow selection attempted to read stdin")
}
