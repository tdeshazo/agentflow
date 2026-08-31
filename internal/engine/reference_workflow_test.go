package engine

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type referenceWorkflowProvider struct {
	calls map[string]int
}

type canonicalSelfHostingProvider struct {
	mu                   sync.Mutex
	calls                map[string]int
	repairImplementation bool
}

func (p *canonicalSelfHostingProvider) Name() string { return "canonical-self-hosting" }
func (p *canonicalSelfHostingProvider) EnforcesFilesystemBoundary() bool {
	return true
}

func (p *canonicalSelfHostingProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	phase := request.Metadata["phase"]
	p.mu.Lock()
	p.calls[phase]++
	call := p.calls[phase]
	p.mu.Unlock()

	var path string
	switch phase {
	case "implement-agentflow-change":
		path = filepath.Join(request.Workspace, "internal", "canonical-implementation.txt")
	case "verify-agentflow-change":
		path = filepath.Join(request.Workspace, "docs", "guides", "canonical-verification.md")
	default:
		return provider.Result{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return provider.Result{}, err
	}
	if err := os.WriteFile(path, []byte(phase+"\n"), 0o644); err != nil {
		return provider.Result{}, err
	}
	if p.repairImplementation && phase == "implement-agentflow-change" {
		marker := filepath.Join(request.Workspace, "internal", "repair-required")
		if call == 1 {
			if err := os.WriteFile(marker, []byte("repair required\n"), 0o644); err != nil {
				return provider.Result{}, err
			}
		} else if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			return provider.Result{}, err
		}
	}
	return provider.Result{}, nil
}

func (p *canonicalSelfHostingProvider) callCount(phase string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[phase]
}

func TestCanonicalSelfHostingWorkflowCompletesTypedAuthorityAndRestartsWithoutReplay(t *testing.T) {
	result := workflow.ValidateFile(canonicalSelfHostingWorkflow(t))
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("canonical workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	repo := canonicalSelfHostingRepository(t)
	p := &canonicalSelfHostingProvider{calls: map[string]int{}}
	newEngine := func() *Engine {
		t.Helper()
		e, err := New(
			result.Normalized.Workflow,
			map[string]provider.Provider{"codex": p},
			Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		e.In = strings.NewReader("")
		e.Out = io.Discard
		return e
	}

	e := newEngine()
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		"baseline-audit",
		"implement-agentflow-change",
		"verify-agentflow-change",
		"integrated-regression-audit",
		"final-implementation-audit",
	} {
		if got := p.callCount(phase); got != 1 {
			t.Errorf("provider calls for %s = %d, want 1", phase, got)
		}
	}
	for _, id := range []string{"implement-agentflow-change", "verify-agentflow-change"} {
		state, ok, err := e.workItemState(id)
		if err != nil || !ok || state.Status != "completed" {
			t.Errorf("work item %q state = %#v, ok=%t, err=%v", id, state, ok, err)
		}
	}
	progress, err := os.ReadFile(filepath.Join(repo, "spec", "agent-workflow-progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(progress), "- [ ]") || strings.Count(string(progress), "- [x]") != 2 {
		t.Fatalf("typed work-item adapter was not completed: %s", progress)
	}
	for phase, evidence := range map[string]string{
		"integrated-regression-audit": "integrated-audit-accepted",
		"final-implementation-audit":  "final-audit-accepted",
	} {
		var record ContractEvidence
		if ok, err := e.Store.GetJSON(e.contractEvidenceRecord(phase, evidence), &record); err != nil || !ok {
			t.Errorf("audit evidence %q: ok=%t err=%v", evidence, ok, err)
		}
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker: ok=%t err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("completed canonical workflow left a dirty workspace: %q", got)
	}

	if err := newEngine().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		"baseline-audit",
		"implement-agentflow-change",
		"verify-agentflow-change",
		"integrated-regression-audit",
		"final-implementation-audit",
	} {
		if got := p.callCount(phase); got != 1 {
			t.Errorf("completed workflow replayed %s: calls=%d", phase, got)
		}
	}
}

func TestCanonicalSelfHostingWorkflowUsesDeclaredOneShotRepair(t *testing.T) {
	result := workflow.ValidateFile(canonicalSelfHostingWorkflow(t))
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("canonical workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	repo := canonicalSelfHostingRepository(t)
	p := &canonicalSelfHostingProvider{calls: map[string]int{}, repairImplementation: true}
	e, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.callCount("implement-agentflow-change"); got != 2 {
		t.Fatalf("implementation actor calls = %d, want primary plus one repair", got)
	}
	if got := p.callCount("verify-agentflow-change"); got != 1 {
		t.Fatalf("verification actor calls = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "internal", "repair-required")); !os.IsNotExist(err) {
		t.Fatalf("repair failure marker survived successful repair: %v", err)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker after repair: ok=%t err=%v", ok, err)
	}
}

func TestCanonicalSelfHostingWorkflowResumesAfterDurableActorCompletion(t *testing.T) {
	result := workflow.ValidateFile(canonicalSelfHostingWorkflow(t))
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("canonical workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	repo := canonicalSelfHostingRepository(t)
	p := &canonicalSelfHostingProvider{calls: map[string]int{}}
	newEngine := func() *Engine {
		t.Helper()
		e, err := New(
			result.Normalized.Workflow,
			map[string]provider.Provider{"codex": p},
			Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
		)
		if err != nil {
			t.Fatal(err)
		}
		e.In = strings.NewReader("")
		e.Out = io.Discard
		return e
	}

	interrupted := newEngine()
	if err := interrupted.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.runPhase(context.Background(), "baseline-audit"); err != nil {
		t.Fatal(err)
	}
	phase, err := interrupted.phaseByID("implement-agentflow-change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := interrupted.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Store.SetJSON(interrupted.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.runPhaseActor(context.Background(), phase, phase.Prompt, &active); err != nil {
		t.Fatal(err)
	}
	if !active.ActorCompleted {
		t.Fatal("successful canonical actor invocation did not persist completion")
	}

	if err := newEngine().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.callCount("implement-agentflow-change"); got != 1 {
		t.Fatalf("safe resume replayed completed implementation actor: calls=%d", got)
	}
	if _, ok, err := interrupted.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker after safe resume: ok=%t err=%v", ok, err)
	}
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

func canonicalSelfHostingWorkflow(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate canonical self-hosting workflow test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "spec", "agent-workflow.yaml")
}

func canonicalSelfHostingRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFile := func(path, contents string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		"CONTRIBUTING.md",
		"GO_STYLE_GUIDE.md",
		"CODE_REVIEW.md",
		"spec/agent-workflow.yaml",
		"spec/agent-workflow-v1alpha1.yaml",
		"skills/agentflow-spec/SKILL.md",
		"ROADMAP.md",
	} {
		writeFile(path, "canonical fixture: "+path+"\n", 0o644)
	}
	writeFile("spec/agent-workflow-progress.md", "# AgentFlow self-hosting work items\n\n- [ ] Implement one bounded AgentFlow specification or interpreter change.\n- [ ] Add focused deterministic tests and documentation for the change.\n", 0o644)
	writeFile("scripts/check.sh", "#!/bin/sh\nset -eu\ntest ! -f internal/repair-required\n", 0o755)
	writeFile(".github/workflows/quality.yml", "jobs:\n  quality:\n    steps:\n      - run: scripts/check.sh\n", 0o644)
	writeFile("docs/planning/README.md", "canonical planning fixture\n", 0o644)
	writeFile("docs/guides/reference.md", "canonical guide fixture\n", 0o644)
	writeFile("internal/.keep", "canonical internal fixture\n", 0o644)
	writeFile("cmd/reference/reference_test.go", "package reference\n", 0o644)
	writeFile("README.md", "canonical README fixture\n", 0o644)
	writeFile("bin/codex", "#!/bin/sh\nexit 0\n", 0o755)

	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "canonical self-hosting fixture")
	t.Setenv("PATH", filepath.Join(repo, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo
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
	var output bytes.Buffer
	e.Out = &output
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Final gate") || !strings.Contains(output.String(), "green") {
		t.Fatalf("completion summary did not report the authored final gate: %q", output.String())
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
