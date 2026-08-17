package workflow

import (
	"path/filepath"
	"testing"
)

func TestLoadPriority5Example(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml")
	d, err := Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NormalizeWorkflow(d)
	if err != nil {
		t.Fatal(err)
	}
	workflow := w.Workflow
	if workflow.Metadata.Name != "complete-priority-5-combat-workflow" {
		t.Fatalf("name = %q", workflow.Metadata.Name)
	}
	if len(workflow.Spec.Phases) != 9 {
		t.Fatalf("phases = %d, want 9", len(workflow.Spec.Phases))
	}
	if got := workflow.Spec.Agents["terra"].Runner; got != "codex" {
		t.Fatalf("terra runner = %q", got)
	}
	if _, ok := workflow.Spec.Completion["priority-5"]; !ok {
		t.Fatal("priority-5 completion missing")
	}
}
