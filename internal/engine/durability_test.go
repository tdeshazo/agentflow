package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
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
	var identity RunIdentity
	if ok, err := e.Store.GetJSON(e.runIdentityRecord(), &identity); err != nil || !ok || identity.Algorithm != "sha256" || identity.WorkflowDigest == "" || identity.ParametersDigest == "" || identity.ExecutionDigest == "" {
		t.Fatalf("run identity: %#v ok=%v err=%v", identity, ok, err)
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

func TestRunIdentityBindsRestartInputs(t *testing.T) {
	newWorkflow := func(repo, name string) *workflow.Workflow {
		w := durableWorkflow(repo, name)
		w.Spec.Flow = nil
		w.Spec.Parameters["task"] = workflow.Parameter{Type: "string", Default: ""}
		w.Spec.Parameters["model"] = workflow.Parameter{Type: "string", Default: "model-a"}
		agent := w.Spec.Agents["worker"]
		agent.Model = "{{ parameters.model }}"
		w.Spec.Agents["worker"] = agent
		return w
	}
	newEngine := func(t *testing.T, w *workflow.Workflow, p provider.Provider, overrides map[string]string) *Engine {
		t.Helper()
		e, err := New(w, map[string]provider.Provider{"test": p}, Options{Overrides: overrides})
		if err != nil {
			t.Fatal(err)
		}
		e.In = strings.NewReader("")
		e.Out = io.Discard
		return e
	}

	t.Run("same task continues", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-same")
		secretTask := "replace the credential rotation path"
		e := newEngine(t, w, &durableProvider{}, map[string]string{"task": secretTask, "model": "model-a"})
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": secretTask, "model": "model-a"}).Run(context.Background()); err != nil {
			t.Fatalf("exact restart: %v", err)
		}
		sha, ok, err := e.Store.Resolve(e.runIdentityRecord())
		if err != nil || !ok {
			t.Fatalf("run identity ref: %q ok=%v err=%v", sha, ok, err)
		}
		blob, err := exec.Command("git", "-C", repo, "cat-file", "blob", sha).CombinedOutput()
		if err != nil {
			t.Fatalf("read run identity blob: %v: %s", err, blob)
		}
		if strings.Contains(string(blob), secretTask) || strings.Contains(string(blob), "model-a") {
			t.Fatalf("run identity persisted a plaintext input: %s", blob)
		}
	})

	t.Run("changed task is rejected without echoing it", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-task")
		oldTask, newTask := "secret task one", "secret task two"
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": oldTask}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := newEngine(t, w, &durableProvider{}, map[string]string{"task": newTask}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "resolved run inputs changed") {
			t.Fatalf("changed task error = %v", err)
		}
		if strings.Contains(err.Error(), oldTask) || strings.Contains(err.Error(), newTask) {
			t.Fatalf("changed task diagnostic exposed a plaintext input: %v", err)
		}
	})

	t.Run("changed model input is rejected", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-model")
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same", "model": "model-a"}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same", "model": "model-b"}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "resolved run inputs changed") {
			t.Fatalf("changed model error = %v", err)
		}
	})

	t.Run("overridden environment-backed parameter does not create a second binding", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-overridden-environment")
		w.Spec.Parameters["model"] = workflow.Parameter{Type: "string", Default: "{{ env.AGENTFLOW_IDENTITY_FALLBACK_MODEL }}"}
		t.Setenv("AGENTFLOW_IDENTITY_FALLBACK_MODEL", "fallback-a")
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same", "model": "chosen"}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AGENTFLOW_IDENTITY_FALLBACK_MODEL", "fallback-b")
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same", "model": "chosen"}).Run(context.Background()); err != nil {
			t.Fatalf("restart with unchanged resolved model: %v", err)
		}
	})

	t.Run("changed executable workflow definition is rejected", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-definition")
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same"}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		agent := w.Spec.Agents["worker"]
		agent.Sandbox = "workspace-write"
		w.Spec.Agents["worker"] = agent
		err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same"}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "executable workflow definition changed") {
			t.Fatalf("changed definition error = %v", err)
		}
	})

	t.Run("changed directly referenced environment is rejected", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-environment")
		w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "{{ env.AGENTFLOW_IDENTITY_MODEL }}", MayCommit: true}
		t.Setenv("AGENTFLOW_IDENTITY_MODEL", "model-a")
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same"}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AGENTFLOW_IDENTITY_MODEL", "model-b")
		err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "same"}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "resolved execution environment changed") {
			t.Fatalf("changed direct environment error = %v", err)
		}
	})

	t.Run("status does not need the original task", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := newWorkflow(repo, "identity-status")
		w.Spec.Parameters["secret"] = workflow.Parameter{Type: "string", Env: "AGENTFLOW_IDENTITY_STATUS_SECRET"}
		if err := newEngine(t, w, &durableProvider{}, map[string]string{"task": "do not persist this task", "secret": "not persisted"}).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{StateOnly: true})
		if err != nil {
			t.Fatalf("construct status without task or secret: %v", err)
		}
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatalf("status without task: %v", err)
		}
		if !strings.Contains(out.String(), "state: ready") {
			t.Fatalf("status = %s", out.String())
		}
	})
}

func TestRunIdentityAllowsIntentionalReset(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "identity-reset")
	w.Spec.Flow = nil
	w.Spec.Parameters["task"] = workflow.Parameter{Type: "string", Default: ""}
	w.Spec.Parameters["reset_run"] = workflow.Parameter{Type: "boolean", Default: false}
	w.Spec.State.Reset.When = "{{ parameters.reset_run }}"
	newEngine := func(t *testing.T, overrides map[string]string) *Engine {
		t.Helper()
		e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{Overrides: overrides})
		if err != nil {
			t.Fatal(err)
		}
		e.In = strings.NewReader("")
		e.Out = io.Discard
		return e
	}

	if err := newEngine(t, map[string]string{"task": "old task"}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	direct, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{StateOnly: true})
	if err != nil {
		t.Fatalf("construct explicit reset without run inputs: %v", err)
	}
	direct.In = strings.NewReader("")
	direct.Out = io.Discard
	if err := direct.Reset(); err != nil {
		t.Fatalf("explicit reset: %v", err)
	}
	if _, ok, err := direct.Store.Resolve(direct.runIdentityRecord()); err != nil || ok {
		t.Fatalf("explicit reset left run identity: ok=%v err=%v", ok, err)
	}
	if err := newEngine(t, map[string]string{"task": "old task"}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	controlled := newEngine(t, map[string]string{"task": "new task", "reset_run": "true"})
	if err := controlled.Run(context.Background()); err != nil {
		t.Fatalf("workflow-controlled reset: %v", err)
	}
	if ok, err := controlled.verifyStoredRunIdentity(); err != nil || !ok {
		t.Fatalf("new identity after workflow-controlled reset: ok=%v err=%v", ok, err)
	}
}

func TestRunIdentityStateNamespacesRemainIsolated(t *testing.T) {
	repo := newDurableRepo(t)
	newWorkflow := func(name string) *workflow.Workflow {
		w := durableWorkflow(repo, name)
		w.Spec.Flow = nil
		w.Spec.Parameters["task"] = workflow.Parameter{Type: "string", Default: ""}
		return w
	}
	newEngine := func(t *testing.T, w *workflow.Workflow, task string) *Engine {
		t.Helper()
		e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{Overrides: map[string]string{"task": task}})
		if err != nil {
			t.Fatal(err)
		}
		e.In = strings.NewReader("")
		e.Out = io.Discard
		return e
	}
	first := newEngine(t, newWorkflow("identity namespace one"), "first")
	second := newEngine(t, newWorkflow("identity-namespace-one"), "second")
	if first.Store.Namespace == second.Store.Namespace {
		t.Fatalf("workflow namespaces collide: %q", first.Store.Namespace)
	}
	if err := first.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := first.Store.Resolve(first.runIdentityRecord()); err != nil || ok {
		t.Fatalf("first identity survived reset: ok=%v err=%v", ok, err)
	}
	if _, ok, err := second.Store.Resolve(second.runIdentityRecord()); err != nil || !ok {
		t.Fatalf("second identity was affected by first reset: ok=%v err=%v", ok, err)
	}
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
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.ActorCompleted {
		t.Fatalf("interrupted actor completion evidence: active=%+v ok=%v err=%v", active, ok, err)
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

func TestResumeInterruptedBeforeImplementationMutationRerunsActor(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-before-first-mutation")
	p := &durableProvider{}
	p.action = func(_ context.Context, request provider.Request) error {
		if p.calls == 1 {
			return context.Canceled
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want context cancellation", err)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.ActorCompleted {
		t.Fatalf("interrupted actor completion evidence: active=%+v ok=%v err=%v", active, ok, err)
	}
	if _, ok, _ := e.Store.Resolve("phases/change"); ok {
		t.Fatal("uninvoked implementation was incorrectly marked complete")
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want actor rerun after pre-mutation interruption", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestResumeInterruptedNoChangeAuditRerunsActor(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-no-change-audit")
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "true"}
	w.Spec.PhaseDefaults.After = []workflow.PhaseAction{
		{Validate: "phaseGate"},
		{Checkpoint: "checkpoint"},
		{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
		{ClearActivePhase: true},
	}
	w.Spec.Phases = []workflow.Phase{{ID: "audit", Kind: "audit", Label: "audit", Actor: "worker", RequiresChange: false, Prompt: "audit"}}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "audit"}, {Complete: "done"}}
	p := &durableProvider{}
	p.action = func(_ context.Context, _ provider.Request) error {
		if p.calls == 1 {
			return context.Canceled
		}
		return nil
	}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted audit error = %v, want context cancellation", err)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.ActorCompleted {
		t.Fatalf("interrupted audit completion evidence: active=%+v ok=%v err=%v", active, ok, err)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want audit actor rerun instead of gate-only acceptance", p.calls)
	}
	if _, ok, err := e.Store.Resolve("phases/audit"); err != nil || !ok {
		t.Fatalf("audit marker: ok=%v err=%v", ok, err)
	}
}

func TestResumeAfterActorCompletionCanAcceptPostCheckpointWithoutRerunningActor(t *testing.T) {
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
	if err := e.Store.SetJSON("active", ActivePhase{PhaseID: "change", StartCommit: start, ActorCompleted: true, CheckpointPending: true}); err != nil {
		t.Fatal(err)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d; completed actor work should be accepted by validation", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestResumeAfterSuccessfulActorReturnBeforeAcceptanceDoesNotReplayActor(t *testing.T) {
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
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || !active.ActorCompleted {
		t.Fatalf("successful actor completion evidence: active=%+v ok=%v err=%v", active, ok, err)
	}
	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d; accepted work was replayed", p.calls)
	}
	assertDurableCompletion(t, e, repo)
}

func TestRuntimeOwnedLifecycleUsesDurableSafetyContract(t *testing.T) {
	t.Run("clean start and resumed acceptance do not replay the actor", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := compactLifecycleWorkflow(repo, "compact-clean-start")
		p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		}}
		e := newDurableEngine(t, w, p)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.Run(ctx); err == nil || !strings.Contains(err.Error(), "validation phaseGate failed") {
			t.Fatalf("interrupted acceptance error = %v", err)
		}
		var active ActivePhase
		if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || !active.ActorCompleted {
			t.Fatalf("durable compact active state = %+v ok=%v err=%v", active, ok, err)
		}
		if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.calls != 1 {
			t.Fatalf("resumed acceptance replayed actor: calls=%d", p.calls)
		}
		assertDurableCompletion(t, e, repo)
	})

	t.Run("dirty partial work is preflighted and preserved before actor resume", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := compactLifecycleWorkflow(repo, "compact-dirty-resume")
		p := &durableProvider{}
		p.action = func(_ context.Context, request provider.Request) error {
			if p.calls == 1 {
				if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644); err != nil {
					return err
				}
				return context.Canceled
			}
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		}
		e := newDurableEngine(t, w, p)
		if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted actor error = %v", err)
		}
		if got := gitIn(t, repo, "status", "--porcelain"); !strings.Contains(got, "work.txt") {
			t.Fatalf("partial work was discarded: %q", got)
		}
		if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.calls != 2 {
			t.Fatalf("actor resume calls=%d, want 2", p.calls)
		}
		assertDurableCompletion(t, e, repo)
	})

	t.Run("checkpoint interruption resumes acceptance without actor work", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := compactLifecycleWorkflow(repo, "compact-checkpoint-resume")
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
		gitIn(t, repo, "commit", "-qm", "partial accepted work")
		if err := e.Store.SetJSON(e.activeRecord(), ActivePhase{
			PhaseID:           "change",
			StartCommit:       start,
			ActorCompleted:    true,
			CheckpointPending: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.calls != 0 {
			t.Fatalf("checkpoint recovery replayed actor: calls=%d", p.calls)
		}
		assertDurableCompletion(t, e, repo)
	})

	t.Run("scope and protected integrity fail before acceptance", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			path string
			want string
		}{
			{name: "scope", path: "not-allowed.txt", want: "out-of-scope file changed"},
			{name: "protected", path: "README.md", want: "protected integrity rule readme changed"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				repo := newDurableRepo(t)
				w := compactLifecycleWorkflow(repo, "compact-"+tc.name)
				if tc.name == "protected" {
					w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{ID: "readme", Paths: []string{"README.md"}, Mode: "exact-hash"}}
				}
				p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
					return os.WriteFile(filepath.Join(request.Workspace, tc.path), []byte("unsafe\n"), 0o644)
				}}
				e := newDurableEngine(t, w, p)
				err := e.Run(context.Background())
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("safety error = %v, want %q", err, tc.want)
				}
				var active ActivePhase
				if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.FailureKind != PhaseFailureSafety {
					t.Fatalf("safety evidence = %+v ok=%v err=%v", active, ok, err)
				}
				if tc.name == "protected" {
					if active.IntegrityViolation == nil || active.IntegrityViolation.IntegrityRule != "readme" || !reflect.DeepEqual(active.IntegrityViolation.Changed, []string{"README.md"}) || len(active.IntegrityViolation.Added) != 0 || len(active.IntegrityViolation.Removed) != 0 {
						t.Fatalf("integrity safety evidence = %#v", active.IntegrityViolation)
					}
					var status bytes.Buffer
					e.Out = &status
					if err := e.Status(); err != nil {
						t.Fatal(err)
					}
					for _, want := range []string{"integrity_rule: readme", "changed:\n  - README.md", "added: []", "removed: []"} {
						if !strings.Contains(status.String(), want) {
							t.Fatalf("integrity text status missing %q:\n%s", want, status.String())
						}
					}
					status.Reset()
					if err := e.StatusJSONTo(&status, false); err != nil {
						t.Fatal(err)
					}
					var projected struct {
						IntegrityRule string   `json:"integrity_rule"`
						Changed       []string `json:"changed"`
						Added         []string `json:"added"`
						Removed       []string `json:"removed"`
					}
					if err := json.Unmarshal(status.Bytes(), &projected); err != nil {
						t.Fatal(err)
					}
					if projected.IntegrityRule != "readme" || !reflect.DeepEqual(projected.Changed, []string{"README.md"}) || projected.Added == nil || projected.Removed == nil {
						t.Fatalf("integrity JSON status = %s", status.String())
					}
				}
			})
		}
	})

	t.Run("lineage and invalid completed markers cannot advance work", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := compactLifecycleWorkflow(repo, "compact-lineage")
		e := newDurableEngine(t, w, &durableProvider{})
		if err := e.initializeOrResumeState(); err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "checkout", "-qb", "other")
		if err := newDurableEngine(t, w, &durableProvider{}).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "differs from workflow branch") {
			t.Fatalf("lineage error = %v", err)
		}

		repo = newDurableRepo(t)
		w = compactLifecycleWorkflow(repo, "compact-marker")
		p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		}}
		e = newDurableEngine(t, w, p)
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "not-allowed.txt"), []byte("tampered\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := newDurableEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "completed workflow is no longer safe") {
			t.Fatalf("invalid completed marker was accepted: %v", err)
		}
		if p.calls != 1 {
			t.Fatalf("invalid completed marker replayed actor: calls=%d", p.calls)
		}
	})
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
		if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.RepairAttempts["phaseGate"] != 1 || active.FailureKind != PhaseFailureValidation {
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

func TestSafetyFailureIsDurableAndDoesNotReplayCompletedActor(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "durable-safety-failure")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.Workspace, "not-allowed.txt"), []byte("unsafe\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "out-of-scope file changed") {
		t.Fatalf("safety failure = %v", err)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || !active.ActorCompleted || active.FailureKind != PhaseFailureSafety {
		t.Fatalf("durable safety state: active=%+v ok=%v err=%v", active, ok, err)
	}
	var out bytes.Buffer
	e.Out = &out
	if err := e.Status(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "state: safety-failed/terminal") || !strings.Contains(out.String(), "actor_completed: true") || !strings.Contains(out.String(), "failure_kind: safety") {
		t.Fatalf("safety status = %s", out.String())
	}
	if !strings.Contains(out.String(), "recovery: operator-action-required") || !strings.Contains(out.String(), "next_action: reset-or-abandon") {
		t.Fatalf("safety recovery status = %s", out.String())
	}
	if !strings.Contains(out.String(), "operator action is required") || !strings.Contains(out.String(), "terminal safety prevents this durable run from continuing") || !strings.Contains(out.String(), "reset the run to start again, or abandon it") {
		t.Fatalf("safety recovery wording = %s", out.String())
	}
	out.Reset()
	if err := e.StatusJSONTo(&out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"state":"safety-failed/terminal"`) ||
		!strings.Contains(out.String(), `"recovery":"operator-action-required"`) ||
		!strings.Contains(out.String(), `"next_action":"reset-or-abandon"`) ||
		strings.Contains(out.String(), "out-of-scope file changed") {
		t.Fatalf("safety JSON compatibility/privacy = %s", out.String())
	}
	if guidance := e.FailureRecoveryGuidance(); !strings.Contains(guidance, "operator action is required") || !strings.Contains(guidance, "Terminal safety prevents this durable run from continuing") || !strings.Contains(guidance, "Reset the run to start again, or abandon it") {
		t.Fatalf("safety guidance = %q", guidance)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "out-of-scope file changed") {
		t.Fatalf("recovered safety failure = %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("safety failure replayed a completed actor: calls = %d", p.calls)
	}

	if err := os.Remove(filepath.Join(repo, "not-allowed.txt")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "work.txt")
	gitIn(t, repo, "commit", "-qm", "manual remediation")
	if err := newDurableEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "out-of-scope file changed") {
		t.Fatalf("rerun after manual remediation = %v, want durable safety failure", err)
	}
	if p.calls != 1 {
		t.Fatalf("manual remediation replayed actor: calls = %d", p.calls)
	}
	if _, ok, err := e.Store.Resolve("phases/change"); err != nil || ok {
		t.Fatalf("manual remediation accepted rejected phase state: ok=%v err=%v", ok, err)
	}
}

func TestRecoverActiveSafetyStopsBeforeValidationAndRepair(t *testing.T) {
	repo := newDurableRepo(t)
	counter := filepath.Join(t.TempDir(), "validation-ran")
	w := durableWorkflow(repo, "active-safety-terminal")
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
	w.Spec.Validation["phaseGate"] = repairValidation()
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf x >> " + counter}
	p := &durableProvider{}
	e := newDurableEngine(t, w, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	active.FailureKind = PhaseFailureSafety
	active.Validation = "phaseGate"
	active.ValidationError = "persisted repository-policy safety failure"
	active.RepairAttempts = map[string]int{"phaseGate": 1}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}

	var safetyErr *safetyViolation
	if err := e.recoverActive(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("recovery error = %v, want durable safety failure", err)
	}
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatalf("recovery ran retained-state validation: stat error = %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("recovery invoked an actor %d times", p.calls)
	}
	var persisted ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &persisted); err != nil || !ok || persisted.FailureKind != PhaseFailureSafety || persisted.RepairAttempts["phaseGate"] != 1 {
		t.Fatalf("persisted terminal state = %+v ok=%v err=%v", persisted, ok, err)
	}
	assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
}

func TestFailureRecoveryGuidanceRequiresDurableActionableState(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "guidance-needs-state")
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("unsafe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("initialization unexpectedly succeeded")
	}
	if guidance := e.FailureRecoveryGuidance(); guidance != "" {
		t.Fatalf("guidance without durable state = %q", guidance)
	}
}

func TestFailureRecoveryGuidanceDoesNotBorrowStateAcrossRunIdentityMismatch(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "guidance-run-identity")
	w.Spec.Parameters["task"] = workflow.Parameter{Type: "string", Default: "old task"}
	p := &durableProvider{action: func(_ context.Context, _ provider.Request) error {
		return context.Canceled
	}}
	first, err := New(w, map[string]provider.Provider{"test": p}, Options{Overrides: map[string]string{"task": "old task"}})
	if err != nil {
		t.Fatal(err)
	}
	first.In = strings.NewReader("")
	first.Out = io.Discard
	if err := first.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("initial interrupted run = %v", err)
	}

	second, err := New(w, map[string]provider.Provider{"test": p}, Options{Overrides: map[string]string{"task": "new task"}})
	if err != nil {
		t.Fatal(err)
	}
	second.In = strings.NewReader("")
	second.Out = io.Discard
	err = second.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "resolved run inputs changed") {
		t.Fatalf("identity mismatch = %v", err)
	}
	if guidance := second.FailureRecoveryGuidance(); guidance != "" {
		t.Fatalf("identity mismatch borrowed recovery guidance = %q", guidance)
	}
}

func TestResumeAfterInterruptedAgentCreatedCommitRerunsActor(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "resume-agent-created-commit")
	p := &durableProvider{}
	p.action = func(_ context.Context, request provider.Request) error {
		if p.calls == 1 {
			if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
				return err
			}
			cmd := exec.Command("git", "-C", request.Workspace, "add", "work.txt")
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("stage agent work: %w: %s", err, output)
			}
			cmd = exec.Command("git", "-C", request.Workspace, "commit", "-qm", "agent: partial work")
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("commit agent work: %w: %s", err, output)
			}
			return context.Canceled
		}
		if got, err := os.ReadFile(filepath.Join(request.Workspace, "work.txt")); err != nil || string(got) != "complete\n" {
			return fmt.Errorf("resumed actor did not receive preserved agent commit: contents=%q err=%v", got, err)
		}
		return nil
	}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want context cancellation", err)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON("active", &active); err != nil || !ok || active.ActorCompleted {
		t.Fatalf("interrupted actor completion evidence: active=%+v ok=%v err=%v", active, ok, err)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want resumed actor after agent-created commit", p.calls)
	}
	if !strings.Contains(gitIn(t, repo, "log", "--format=%s"), "agent: partial work") {
		t.Fatal("agent-created partial commit was not preserved")
	}
	assertDurableCompletion(t, e, repo)
}

func TestPhaseMarkerIsAuthoritativeBeforeActiveStateCleanup(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "marker-before-active-cleanup")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhase(phase.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON("active", active); err != nil {
		t.Fatal(err)
	}
	if err := e.runPhaseActor(context.Background(), phase, phase.Prompt, &active); err != nil {
		t.Fatal(err)
	}
	if err := e.checkpoint(phase.Label, phase); err != nil {
		t.Fatal(err)
	}
	if err := e.markPhaseComplete(phase); err != nil {
		t.Fatal(err)
	}

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want no replay after phase marker", p.calls)
	}
	assertDurableCompletion(t, e, repo)
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

func TestCheckpointUsesDescriptiveDefaultCommitSubject(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "descriptive-checkpoint")
	w.Spec.Workspace.Checkpointing.CommitMessage = ""
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.checkpoint("implement-actionable-error-recovery", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitIn(t, repo, "log", "-1", "--format=%s")); got != "Implement actionable error recovery" {
		t.Fatalf("checkpoint commit subject = %q", got)
	}
}

func TestCheckpointExpandsDefaultCommitSubjectLabelBeforeFormatting(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "templated-descriptive-checkpoint")
	w.Spec.Workspace.Checkpointing.CommitMessage = ""
	w.Spec.Parameters["issue_id"] = workflow.Parameter{Type: "string", Default: "agentflow_123"}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	phase := &workflow.Phase{Label: "implement-{{ parameters.issue_id }}"}
	if err := e.checkpoint("checkpoint", phase); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitIn(t, repo, "log", "-1", "--format=%s")); got != "Implement agentflow 123" {
		t.Fatalf("checkpoint commit subject = %q", got)
	}
}

func TestCheckpointUsesNonEmptyDefaultCommitSubject(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "unlabeled-checkpoint")
	w.Spec.Workspace.Checkpointing.CommitMessage = ""
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.checkpoint("", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitIn(t, repo, "log", "-1", "--format=%s")); got != "Record workflow changes" {
		t.Fatalf("checkpoint commit subject = %q", got)
	}
}

func TestValidationDoesNotAcceptAnEmptyPostRepairSequence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "repair-must-rerun-gate")
	v := repairValidation()
	v.OnFailure.Then = nil // Omission means re-run the original deterministic steps.
	w.Spec.Validation["phaseGate"] = v
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "still fails after repair") {
		t.Fatalf("validation error = %v, want failed post-repair gate", err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want implementation plus one repair", p.calls)
	}
	if _, ok, _ := e.Store.Resolve("phases/change"); ok {
		t.Fatal("empty post-repair sequence accepted a failed phase")
	}
}

func TestStandaloneValidationRepairBudgetSurvivesRestart(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "standalone-repair-budget")
	w.Spec.Flow = []workflow.FlowStep{{Validate: "phaseGate"}}
	w.Spec.Validation["phaseGate"] = repairValidation()
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
	p := &durableProvider{}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "still fails after repair") {
		t.Fatalf("first validation error = %v, want failed repair", err)
	}
	if p.calls != 1 {
		t.Fatalf("first run provider calls = %d, want one repair", p.calls)
	}
	if err := newDurableEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
		t.Fatalf("restart error = %v, want durable repair exhaustion", err)
	}
	if p.calls != 1 {
		t.Fatalf("restart invoked another repair actor: calls = %d", p.calls)
	}
}

func TestPhaseCannotBeAcceptedWithoutValidation(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "validation-required-for-acceptance")
	w.Spec.PhaseDefaults.After = []workflow.PhaseAction{
		{Checkpoint: "checkpoint"},
		{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
		{ClearActivePhase: true},
	}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "no deterministic validation before acceptance") {
		t.Fatalf("phase without validation error = %v", err)
	}
	if _, ok, _ := e.Store.Resolve("phases/change"); ok {
		t.Fatal("phase without validation received an acceptance marker")
	}
}

func TestPhaseCannotSkipItsOnlyValidationBeforeAcceptance(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "validation-must-actually-run")
	w.Spec.PhaseDefaults.After = []workflow.PhaseAction{
		{Validate: "phaseGate", If: "{{ false }}"},
		{Checkpoint: "checkpoint"},
		{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
		{ClearActivePhase: true},
	}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "did not run a successful deterministic validation") {
		t.Fatalf("conditionally skipped validation error = %v", err)
	}
	if _, ok, _ := e.Store.Resolve("phases/change"); ok {
		t.Fatal("phase with skipped validation received an acceptance marker")
	}
}

func TestMutationPolicyLineageIsEnforcedOnResume(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "mutation-policy-lineage")
	w.Spec.State.Lineage = workflow.StateLineage{}
	w.Spec.State.Resume = workflow.StateResume{Enabled: boolPtr(true)}
	w.Spec.Workspace.MutationPolicy.Lineage.RequireSameBranchAsState = true
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "checkout", "-qb", "other")
	if err := newDurableEngine(t, w, &durableProvider{}).initializeOrResumeState(); err == nil || !strings.Contains(err.Error(), "differs from workflow branch") {
		t.Fatalf("mutation-policy lineage error = %v", err)
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

func TestHumanGateEvidenceIsRestartIdempotent(t *testing.T) {
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
	if p.calls != 0 {
		t.Fatalf("completed workflow invoked a provider: %d calls", p.calls)
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
			State: workflow.StateSpec{
				Initialize: workflow.StateInitialize{RequireCleanImplementationWorkspace: true, RequireNamedBranch: true},
				Resume:     workflow.StateResume{Enabled: boolPtr(true), RequireBaseIsAncestorOfHead: true, RequireSameBranch: true},
				Lineage:    workflow.StateLineage{RequireBaseCommitExists: true, RequireBaseIsAncestorOfHead: true, RequireSameNamedBranch: true},
				Reset:      workflow.StateReset{RequireCleanImplementationWorkspace: true},
			},
			Workspace: workflow.WorkspaceSpec{
				Root:           "{{ parameters.repo_root }}",
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt"}},
				Checkpointing:  workflow.CheckpointSpec{CommitMessage: "checkpoint: {{ phase.label }}"},
			},
			Agents: map[string]workflow.Agent{"worker": {Runner: "test", MayCommit: true}},
			Tools: map[string]workflow.Tool{
				"scope":      {Type: "workspace-policy"},
				"gate":       {Type: "shell", Command: "grep -qx complete work.txt"},
				"checkpoint": {Type: "git-checkpoint"},
			},
			Validation: map[string]workflow.Validation{
				"phaseGate": {Steps: []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}}},
			},
			PhaseDefaults: workflow.PhaseDefaults{
				After: []workflow.PhaseAction{
					{Validate: "phaseGate"},
					{Checkpoint: "checkpoint"},
					{AssertNetRepositoryChangeSincePhaseStart: true},
					{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
					{ClearActivePhase: true},
				},
			},
			Phases: []workflow.Phase{{ID: "change", Kind: "implementation", Label: "change", Actor: "worker", RequiresChange: true, Prompt: "complete the work"}},
			Flow:   []workflow.FlowStep{{Phase: "change"}, {Complete: "done"}},
			Completion: map[string]workflow.Completion{"done": {
				Assertions: []workflow.Assertion{{Type: "implementation-workspace-clean"}},
			}},
		},
	}
}

func compactLifecycleWorkflow(repo, name string) *workflow.Workflow {
	w := durableWorkflow(repo, name)
	w.Spec.Lifecycle = workflow.LifecyclePolicy{Policy: "safe-resume", Validation: "phaseGate"}
	w.Spec.PhaseDefaults = workflow.PhaseDefaults{}
	w.Spec.Phases[0].Validation = ""
	return w
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
