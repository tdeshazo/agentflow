package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

type durableProvider struct {
	calls  int
	action func(context.Context, provider.Request) error
}

func (p *durableProvider) Name() string { return "durable-test" }
func (p *durableProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	p.calls++
	if p.action != nil {
		return provider.Result{}, p.action(ctx, request)
	}
	return provider.Result{}, nil
}

func TestInitializeRequiresCleanWorkspaceAndCapturesState(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "initialization")
	w.Spec.Flow = nil
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "first run requires clean implementation workspace") {
		t.Fatalf("initial dirty run error = %v", err)
	}
	if _, ok, err := e.Store.Resolve("base"); err != nil || ok {
		t.Fatalf("dirty initialization created base state: ok=%v err=%v", ok, err)
	}

	gitIn(t, repo, "add", "work.txt")
	gitIn(t, repo, "commit", "-qm", "make workspace clean")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	base, ok, err := e.Store.Resolve("base")
	if err != nil || !ok {
		t.Fatalf("base state: %q ok=%v err=%v", base, ok, err)
	}
	if got := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD")); got != base {
		t.Fatalf("base = %s, want current head %s", base, got)
	}
	var branch string
	if ok, err := e.Store.GetJSON("branch", &branch); err != nil || !ok || branch == "" {
		t.Fatalf("branch state: %q ok=%v err=%v", branch, ok, err)
	}
	var integrity IntegrityBaseline
	if ok, err := e.Store.GetJSON("integrity", &integrity); err != nil || !ok {
		t.Fatalf("integrity baseline: ok=%v err=%v", ok, err)
	}

	t.Run("interrupted initialization is retried only before execution evidence exists", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "interrupted-initialization")
		w.Spec.Flow = nil
		e := newDurableEngine(t, w, &durableProvider{})
		head, err := e.Repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SetCommit("base", head); err != nil {
			t.Fatal(err)
		}
		if err := newDurableEngine(t, w, &durableProvider{}).Run(context.Background()); err != nil {
			t.Fatalf("resume interrupted initialization: %v", err)
		}
		var branch string
		if ok, err := e.Store.GetJSON("branch", &branch); err != nil || !ok || branch == "" {
			t.Fatalf("recovered initialization branch: %q ok=%v err=%v", branch, ok, err)
		}
		if got, err := e.Repo.Head(); err != nil || got != head {
			t.Fatalf("recovered initialization rewrote history: head=%s err=%v want=%s", got, err, head)
		}
	})
}

func TestResumeInterruptedBeforeCheckpointPreservesDirtyPartialWork(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-before-checkpoint")
	p := &durableProvider{}
	p.action = func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644); err != nil {
			return err
		}
		if p.calls == 1 {
			return context.Canceled
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want context cancellation", err)
	}
	if ok, err := e.Store.GetJSON("active", &ActivePhase{}); err != nil || !ok {
		t.Fatalf("active state after interruption: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); !strings.Contains(got, "work.txt") {
		t.Fatalf("partial work was not retained: %q", got)
	}
	if _, ok, _ := e.Store.Resolve("phases/change"); ok {
		t.Fatal("interrupted partial work was incorrectly marked complete")
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want resume call", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestResumeAcceptsInterruptedPostCheckpointWithoutRerunningAgent(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-after-checkpoint")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test"}
	p := &durableProvider{}
	e := newDurableEngine(t, w, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "work.txt")
	gitIn(t, repo, "commit", "-qm", "checkpointed work")
	if err := e.Store.SetJSON("active", ActivePhase{PhaseID: "change", StartCommit: start, CheckpointPending: true}); err != nil {
		t.Fatal(err)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d; post-checkpoint work should be accepted by validation", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestResumeAfterInterruptedValidationDoesNotReplayAcceptedWork(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-interrupted-validation")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Run(ctx); err == nil || !strings.Contains(err.Error(), "validation phaseGate failed") {
		t.Fatalf("cancelled validation error = %v", err)
	}
	if ok, err := e.Store.GetJSON("active", &ActivePhase{}); err != nil || !ok {
		t.Fatalf("active state after validation interruption: ok=%v err=%v", ok, err)
	}
	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d; accepted work was replayed", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestValidationRepairsOnceAndExhaustionSurvivesRestart(t *testing.T) {
	t.Run("repair can make the same phase acceptable", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "repair-success")
		w.Spec.Validation["phaseGate"] = repairValidation()
		w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
		p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
			contents := "partial\n"
			if request.Metadata["actor"] == "repair" {
				contents = "complete\n"
			}
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte(contents), 0o644)
		}}
		e := newDurableEngine(t, w, p)
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.calls != 2 {
			t.Fatalf("provider calls = %d, want one main actor and one repair", p.calls)
		}
		assertDurableCompletion(t, e, repo)
	})

	t.Run("a failed repair is not renewed by restarting", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "repair-exhausted")
		w.Spec.Validation["phaseGate"] = repairValidation()
		w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
		p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
		}}
		e := newDurableEngine(t, w, p)
		if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "still fails after repair") {
			t.Fatalf("first failing validation error = %v", err)
		}
		if p.calls != 2 {
			t.Fatalf("first run calls = %d, want main plus one repair", p.calls)
		}
		var active ActivePhase
		if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.RepairAttempts["phaseGate"] != 1 {
			t.Fatalf("repair budget was not persisted: active=%+v ok=%v err=%v", active, ok, err)
		}
		if err := newDurableEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("restart error = %v, want exhausted repair budget", err)
		}
		if p.calls != 2 {
			t.Fatalf("restart invoked an actor after repair exhaustion: total calls = %d", p.calls)
		}
		if _, ok, _ := e.Store.Resolve("phases/change"); ok {
			t.Fatal("failed validation was marked as an accepted phase")
		}
	})
}

func TestAgentCreatedCommitIsPreservedAsCheckpointEvidence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "agent-commit")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		cmd := exec.Command("git", "-C", request.Workspace, "add", "work.txt")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("stage agent work: %w: %s", err, output)
		}
		cmd = exec.Command("git", "-C", request.Workspace, "commit", "-qm", "agent: complete work")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("commit agent work: %w: %s", err, output)
		}
		return nil
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d", p.calls)
	}
	if !strings.Contains(gitIn(t, repo, "log", "--format=%s", "-1"), "agent: complete work") {
		t.Fatal("agent-created commit was not preserved at HEAD")
	}
	assertDurableCompletion(t, e, repo)
}

func TestCheckpointDoesNotAcceptUnrelatedPreStagedControlFiles(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "checkpoint-index-scope")
	w.Spec.Workspace.LocalControl.Ignored = []string{"control.txt"}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "control.txt"), []byte("local control\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "control.txt")
	if err := e.checkpoint("implementation", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gitIn(t, repo, "show", "--format=", "--name-only", "HEAD"), "control.txt") {
		t.Fatal("checkpoint accepted an unrelated pre-staged control file")
	}
	if got := gitIn(t, repo, "status", "--porcelain"); !strings.Contains(got, "control.txt") {
		t.Fatalf("checkpoint discarded pre-staged local control work: %q", got)
	}
}

func TestInvalidatedMarkerAndLineageChangesDoNotAdvanceWork(t *testing.T) {
	t.Run("invalidated phase marker is rerun", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "invalidated-marker")
		p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		}}
		e := newDurableEngine(t, w, p)
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		base, _, err := e.Store.Resolve("base")
		if err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "reset", "--hard", base)
		if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.calls != 2 {
			t.Fatalf("provider calls = %d, want replay after invalidated marker", p.calls)
		}
		assertDurableCompletion(t, e, repo)
	})

	for _, tc := range []struct {
		name       string
		change     func(*testing.T, string)
		errContain string
	}{
		{
			name:       "branch change",
			change:     func(t *testing.T, repo string) { gitIn(t, repo, "checkout", "-qb", "other") },
			errContain: "differs from workflow branch",
		},
		{
			name:       "detached head",
			change:     func(t *testing.T, repo string) { gitIn(t, repo, "checkout", "--detach") },
			errContain: "detached HEAD",
		},
		{
			name: "non ancestor rewritten history",
			change: func(t *testing.T, repo string) {
				gitIn(t, repo, "checkout", "--orphan", "rewritten")
				gitIn(t, repo, "rm", "-rf", ".")
				if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("rewritten\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitIn(t, repo, "add", "README.md")
				gitIn(t, repo, "commit", "-qm", "rewritten root")
			},
			errContain: "no longer descends from workflow base",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := durableWorkflow(repo, "lineage-"+strings.ReplaceAll(tc.name, " ", "-"))
			e := newDurableEngine(t, w, &durableProvider{})
			if err := e.initializeOrResumeState(); err != nil {
				t.Fatal(err)
			}
			tc.change(t, repo)
			if err := newDurableEngine(t, w, &durableProvider{}).Run(context.Background()); err == nil || !strings.Contains(err.Error(), tc.errContain) {
				t.Fatalf("lineage error = %v, want %q", err, tc.errContain)
			}
		})
	}
}

func TestHumanGateAndCompletionAreRestartIdempotent(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "human-and-complete")
	w.Spec.Phases = nil
	w.Spec.Flow = []workflow.FlowStep{{Human: "review"}}
	w.Spec.HumanGates = []workflow.HumanGate{{
		ID:              "review",
		When:            "{{ true }}",
		Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "yes"},
	}}
	p := &durableProvider{}
	e := newDurableEngine(t, w, p)
	e.In = strings.NewReader("yes\n")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.Store.Resolve("human/review"); err != nil || !ok {
		t.Fatalf("human evidence: ok=%v err=%v", ok, err)
	}

	w.Spec.Flow = []workflow.FlowStep{{Human: "review"}, {Complete: "done"}}
	var out bytes.Buffer
	e2 := newDurableEngine(t, w, p)
	e2.In = strings.NewReader("")
	e2.Out = &out
	if err := e2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already recorded") {
		t.Fatalf("restart did not use durable human evidence: %s", out.String())
	}
	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("completed workflow invoked a provider: %d calls", p.calls)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("complete evidence: ok=%v err=%v", ok, err)
	}
}

func TestResetPreservesRepositoryHistoryAndRejectsDirtyState(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Reset(); err == nil || !strings.Contains(err.Error(), "reset requires clean implementation workspace") {
		t.Fatalf("dirty reset error = %v", err)
	}
	if _, ok, _ := e.Store.Resolve("base"); !ok {
		t.Fatal("dirty reset removed state")
	}
	gitIn(t, repo, "clean", "-fd")
	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.Store.Resolve("base"); ok {
		t.Fatal("reset left workflow state behind")
	}
	if got := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD")); got != head {
		t.Fatalf("reset rewrote repository history: head=%s want=%s", got, head)
	}
}

func durableWorkflow(repo, name string) *workflow.Workflow {
	return &workflow.Workflow{
		Metadata: workflow.Metadata{Name: name},
		Spec: workflow.Spec{
			Parameters: map[string]workflow.Parameter{"repo_root": {Type: "path", Default: repo}},
			State:      workflow.StateSpec{Resume: workflow.StateResume{Enabled: boolPtr(true)}},
			Workspace: workflow.WorkspaceSpec{
				Root:           "{{ parameters.repo_root }}",
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt"}},
				Checkpointing:  workflow.CheckpointSpec{CommitMessage: "checkpoint: {{ phase.label }}"},
			},
			Agents: map[string]workflow.Agent{"worker": {Runner: "test", MayCommit: true}},
			Tools: map[string]workflow.Tool{
				"scope": {Type: "workspace-policy"},
				"gate":  {Type: "shell", Command: "grep -qx complete work.txt"},
			},
			Validation: map[string]workflow.Validation{
				"phaseGate": {Steps: []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}}},
			},
			Phases: []workflow.Phase{{ID: "change", Kind: "implementation", Label: "change", Actor: "worker", RequiresChange: true, Prompt: "complete the work"}},
			Flow:   []workflow.FlowStep{{Phase: "change"}, {Complete: "done"}},
			Completion: map[string]workflow.Completion{"done": {
				Assertions: []workflow.Assertion{{Type: "implementation-workspace-clean"}},
			}},
		},
	}
}

func boolPtr(v bool) *bool { return &v }

func repairValidation() workflow.Validation {
	return workflow.Validation{
		Steps: []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}},
		OnFailure: workflow.FailurePolicy{
			Strategy:          "repair-once",
			MaxRepairAttempts: 1,
			Repair:            workflow.Repair{Actor: "repair", Prompt: "repair the work"},
			Then:              []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}},
		},
	}
}

func newDurableRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "README.md")
	gitIn(t, repo, "commit", "-qm", "seed")
	return repo
}

func newDurableEngine(t *testing.T, w *workflow.Workflow, p provider.Provider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func assertDurableCompletion(t *testing.T, e *Engine, repo string) {
	t.Helper()
	if _, ok, err := e.Store.Resolve("phases/change"); err != nil || !ok {
		t.Fatalf("phase marker: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("complete marker: ok=%v err=%v", ok, err)
	}
	if ok, err := e.Store.GetJSON("active", &ActivePhase{}); err != nil || ok {
		t.Fatalf("active state after completion: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("workspace remains dirty: %q", got)
	}
}
