package workflow

import (
	"path/filepath"
	"testing"
)

func TestLoadPriority5Example(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml")
	w, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if w.Metadata.Name != "complete-priority-5-combat-workflow" {
		t.Fatalf("name = %q", w.Metadata.Name)
	}
	if len(w.Spec.Phases) != 9 {
		t.Fatalf("phases = %d, want 9", len(w.Spec.Phases))
	}
	if got := w.Spec.Agents["terra"].Runner; got != "codex" {
		t.Fatalf("terra runner = %q", got)
	}
	if _, ok := w.Spec.Completion["priority-5"]; !ok {
		t.Fatal("priority-5 completion missing")
	}
}
