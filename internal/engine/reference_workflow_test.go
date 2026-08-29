package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type referenceWorkflowProvider struct {
	calls map[string]int
}

func (p *referenceWorkflowProvider) Name() string                     { return "reference-workflow" }
func (p *referenceWorkflowProvider) EnforcesFilesystemBoundary() bool { return true }

func (p *referenceWorkflowProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	phase := request.Metadata["phase"]
	p.calls[phase]++
	if phase != "01" && phase != "02" {
		return provider.Result{}, nil
	}
	path := filepath.Join(request.Workspace, "internal", "reference-"+phase+".txt")
	if err := os.WriteFile(path, []byte("implemented\n"), 0o644); err != nil {
		return provider.Result{}, err
	}
	return provider.Result{}, nil
}

func TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle(t *testing.T) {
	reference := referenceWorkflow(t)
	result := workflow.ValidateFile(reference)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("reference workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	repo := referenceWorkflowRepository(t)
	p := &referenceWorkflowProvider{calls: map[string]int{}}
	e, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{
			RepoRoot: repo,
			Overrides: map[string]string{
				"require_human_verification": "false",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls["01"] != 1 || p.calls["02"] != 1 || p.calls["07"] != 1 || p.calls["08"] != 1 {
		t.Fatalf("provider calls = %#v, want one call for every actor-owned phase", p.calls)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker: ok=%t err=%v", ok, err)
	}

	progress, err := os.ReadFile(filepath.Join(repo, "docs", "planning", "roadmap-current.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(progress), "- [ ]") || !strings.Contains(string(progress), "Status: Complete") {
		t.Fatalf("runtime-owned progress/bookkeeping did not complete: %s", progress)
	}
	index, err := os.ReadFile(filepath.Join(repo, "docs", "planning", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "5. [x] Stage 1B runtime parity") {
		t.Fatalf("runtime-owned index transition missing: %s", index)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("completed reference workflow left a dirty workspace: %q", got)
	}

	restarted, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	restarted.In = strings.NewReader("")
	restarted.Out = io.Discard
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls["01"] != 1 || p.calls["02"] != 1 || p.calls["07"] != 1 || p.calls["08"] != 1 {
		t.Fatalf("completed reference workflow replayed an actor: %#v", p.calls)
	}
}

func TestReferenceV1Alpha1SafeResumeRecoversRetainedWorkWithoutProceduralRecovery(t *testing.T) {
	reference := referenceWorkflow(t)
	result := workflow.ValidateFile(reference)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("reference workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	if len(result.Document.Workflow.Spec.Recovery.ActivePhase) != 0 {
		t.Fatalf("reference workflow declares procedural recovery: %#v", result.Document.Workflow.Spec.Recovery.ActivePhase)
	}

	repo := referenceWorkflowRepository(t)
	p := &referenceWorkflowProvider{calls: map[string]int{}}
	first, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := first.phaseByID("01")
	if err != nil {
		t.Fatal(err)
	}
	active, err := first.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Store.SetJSON(first.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := first.runPhaseActor(context.Background(), phase, phase.Prompt, &active); err != nil {
		t.Fatal(err)
	}
	if !active.ActorCompleted {
		t.Fatal("successful actor invocation did not persist actor_completed")
	}

	resumed, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	resumed.In = strings.NewReader("")
	resumed.Out = io.Discard
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatalf("safe-resume failed: %v", err)
	}
	if p.calls["01"] != 1 {
		t.Fatalf("safe-resume replayed phase 01 actor %d times", p.calls["01"])
	}
	for _, phase := range []string{"02", "07", "08"} {
		if p.calls[phase] != 1 {
			t.Fatalf("phase %s calls = %d, want 1", phase, p.calls[phase])
		}
	}
}

func TestReferenceV1Alpha1CanonicalGateEvidenceReusesAndInvalidates(t *testing.T) {
	reference := referenceWorkflow(t)
	result := workflow.ValidateFile(reference)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("reference workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	repo := referenceWorkflowRepository(t)
	counter := filepath.Join(t.TempDir(), "canonical-gate-invocations")
	t.Setenv("AGENTFLOW_REFERENCE_GATE_COUNTER", counter)
	e, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": &referenceWorkflowProvider{calls: map[string]int{}}},
		Options{RepoRoot: repo},
	)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatalf("canonical gate invocations after identical validation = %d, want 1", got)
	}
	if err := os.WriteFile(filepath.Join(repo, "internal", ".keep"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "phaseGate", nil); err != nil {
		t.Fatalf("changed declared dependency did not rerun canonical gate: %v", err)
	}
	if got := validationInvocationCount(t, counter); got != 2 {
		t.Fatalf("canonical gate invocations after dependency change = %d, want 2", got)
	}
}

func referenceWorkflow(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate reference workflow test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "spec", "agent-workflow-v1alpha1.yaml")
}

func referenceWorkflowRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeReferenceFile := func(path, contents string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	writeReferenceFile("AGENTS.md", "reference fixture instructions\n", 0o644)
	writeReferenceFile("scripts/check.sh", "#!/bin/sh\nset -eu\ntest -f .agentflow-reference-gate\nif [ -n \"${AGENTFLOW_REFERENCE_GATE_COUNTER:-}\" ]; then\n  printf x >> \"$AGENTFLOW_REFERENCE_GATE_COUNTER\"\nfi\n", 0o755)
	writeReferenceFile(".github/workflows/quality.yml", "jobs:\n  quality:\n    steps:\n      - run: scripts/check.sh\n", 0o644)
	writeReferenceFile(".agentflow-reference-gate", "ready\n", 0o644)
	writeReferenceFile("docs/planning/roadmap-current.md", "Status: In progress\n- [ ] First acceptance criterion text\n- [ ] Second acceptance criterion text\n", 0o644)
	writeReferenceFile("docs/planning/README.md", "5. [ ] Stage 1B runtime parity\n", 0o644)
	writeReferenceFile("internal/.keep", "fixture\n", 0o644)
	writeReferenceFile("bin/codex", "#!/bin/sh\nexit 0\n", 0o755)

	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "reference fixture")
	t.Setenv("PATH", filepath.Join(repo, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo
}
