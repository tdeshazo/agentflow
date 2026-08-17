package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

func TestValidationEvidenceReusesOnlyEquivalentDeclaredState(t *testing.T) {
	repo := newDurableRepo(t)
	counter := filepath.Join(t.TempDir(), "invocations")
	if err := os.WriteFile(filepath.Join(repo, "input.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "input.txt", "other.txt")
	gitIn(t, repo, "commit", "-qm", "inputs")

	w := durableWorkflow(repo, "validation-evidence-equivalence")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"README.md", "input.txt", "other.txt"}
	w.Spec.Parameters["counter"] = workflow.Parameter{Type: "path", Default: counter}
	w.Spec.Tools["gate"] = workflow.Tool{
		Type:    "shell",
		Command: "test -s input.txt && printf x >> {{ parameters.counter }}",
	}
	w.Spec.Validation["phaseGate"] = workflow.Validation{
		Dependencies: []string{"input.txt", "other.txt"},
		Steps:        []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}},
	}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}

	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatal(err)
	}
	if got := validationInvocationCount(t, counter); got != 1 {
		t.Fatalf("identical validation invocations = %d, want 1", got)
	}

	// An undeclared file is not part of this gate's relevant tree.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatalf("unrelated tree change invalidated evidence: %v", err)
	}
	if got := validationInvocationCount(t, counter); got != 1 {
		t.Fatalf("unrelated tree change invoked validation %d times, want 1", got)
	}

	for _, tc := range []struct {
		name string
		path string
		text string
	}{
		{name: "first declared dependency", path: "input.txt", text: "two\n"},
		{name: "second declared dependency", path: "other.txt", text: "two\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(repo, tc.path), []byte(tc.text), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	if got := validationInvocationCount(t, counter); got != 3 {
		t.Fatalf("declared dependency changes invoked validation %d times, want 3", got)
	}
}

func TestValidationEvidenceSurvivesRestartButNotSafetyBoundary(t *testing.T) {
	repo := newDurableRepo(t)
	counter := filepath.Join(t.TempDir(), "invocations")
	if err := os.WriteFile(filepath.Join(repo, "input.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "input.txt")
	gitIn(t, repo, "commit", "-qm", "input")
	w := durableWorkflow(repo, "validation-evidence-restart")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"input.txt"}
	w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{ID: "readme", Paths: []string{"README.md"}, Mode: "exact-hash"}}
	w.Spec.Parameters["counter"] = workflow.Parameter{Type: "path", Default: counter}
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "grep -qx ok input.txt && printf x >> {{ parameters.counter }}"}
	w.Spec.Validation["phaseGate"] = workflow.Validation{
		Dependencies: []string{"input.txt"},
		Steps:        []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}},
	}
	first := newDurableEngine(t, w, &durableProvider{})
	if err := first.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := first.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatal(err)
	}

	// A new Engine models a process restart. It must see the same evidence but
	// must still execute the safety boundary before trusting it.
	second := newDurableEngine(t, w, &durableProvider{})
	if err := second.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := second.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatalf("restart did not reuse durable validation evidence: %v", err)
	}
	if got := validationInvocationCount(t, counter); got != 1 {
		t.Fatalf("restart invoked validation %d times, want 1", got)
	}

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := second.runValidation(context.Background(), "phaseGate", nil); err == nil || !strings.Contains(err.Error(), "protected integrity rule readme changed") {
		t.Fatalf("safety boundary result = %v", err)
	}
	if got := validationInvocationCount(t, counter); got != 1 {
		t.Fatalf("safety violation invoked cached validation %d times, want 1", got)
	}
}

func TestValidationEvidenceIsWrittenOnlyAfterRepairRerunsTheGate(t *testing.T) {
	repo := newDurableRepo(t)
	counter := filepath.Join(t.TempDir(), "invocations")
	w := durableWorkflow(repo, "validation-evidence-repair")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"input.txt"}
	w.Spec.Parameters["counter"] = workflow.Parameter{Type: "path", Default: counter}
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf x >> {{ parameters.counter }}; test -f input.txt"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test"}
	w.Spec.Validation["phaseGate"] = workflow.Validation{
		Dependencies: []string{"input.txt"},
		Steps:        []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}},
		OnFailure: workflow.FailurePolicy{
			Strategy:          "repair-once",
			MaxRepairAttempts: 1,
			Repair:            workflow.Repair{Actor: "worker", Prompt: "repair"},
		},
	}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "input.txt"), []byte("repaired\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatal(err)
	}
	if got := validationInvocationCount(t, counter); got != 2 {
		t.Fatalf("gate invocations after repair = %d, want failed run plus rerun", got)
	}
	if p.calls != 1 {
		t.Fatalf("repair actor calls = %d, want 1", p.calls)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatal(err)
	}
	if got := validationInvocationCount(t, counter); got != 2 {
		t.Fatalf("post-repair validation was not reused: invocations=%d", got)
	}
}

func TestValidationFailureEvidenceIsBoundedAndRedacted(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "validation-failure-output")
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf 'TOKEN=super-secret\\nFULL_ENV=complete-environment\\n' >&2; false"}
	w.Spec.Validation["phaseGate"] = workflow.Validation{Steps: []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}}}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err == nil {
		t.Fatal("failing validation unexpectedly passed")
	}
	var failure validationFailureEvidence
	if ok, err := e.Store.GetJSON(e.standaloneFailureRecord("phaseGate"), &failure); err != nil || !ok {
		t.Fatalf("failure evidence: %+v ok=%v err=%v", failure, ok, err)
	}
	if strings.Contains(failure.Output, "super-secret") || strings.Contains(failure.Output, "complete-environment") {
		t.Fatalf("failure evidence retained sensitive output: %q", failure.Output)
	}
	if len(failure.Output) > maxValidationFailureOutput+64 {
		t.Fatalf("failure evidence was not bounded: %d", len(failure.Output))
	}
}

func validationInvocationCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}
