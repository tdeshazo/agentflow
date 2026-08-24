package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeRejectsWorkflowOwnedColorPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "color.agent-workflow.yaml")
	const document = `apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: color-policy
spec:
  agents:
    worker:
      runner: codex
      color: never
`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Decode(path)
	if err == nil {
		t.Fatal("Decode accepted workflow-owned color policy")
	}
	if !strings.Contains(err.Error(), "field color not found") {
		t.Fatalf("Decode error = %q, want unknown color field", err)
	}
}
