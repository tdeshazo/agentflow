package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type schedulingProvider struct {
	calls  []string
	action func(context.Context, provider.Request) error
}

func (p *schedulingProvider) Name() string { return "scheduler-test" }

func (p *schedulingProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	phase := request.Metadata["phase"]
	p.calls = append(p.calls, phase+":"+request.Metadata["actor"])
	if p.action != nil {
		if err := p.action(ctx, request); err != nil {
			return provider.Result{}, err
		}
	}
	if phase == "" {
		return provider.Result{}, nil
	}
	return provider.Result{}, os.WriteFile(filepath.Join(request.Workspace, phase+".txt"), []byte(phase+"\n"), 0o644)
}

func TestV1Alpha2SerialReadyNodeScheduler(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		deps map[string][]string
		want []string
	}{
		{name: "one phase", ids: []string{"one"}, want: []string{"one:worker"}},
		{name: "linear chain", ids: []string{"design", "build", "review"}, deps: map[string][]string{"build": {"design"}, "review": {"build"}}, want: []string{"design:worker", "build:worker", "review:worker"}},
		{name: "fan out", ids: []string{"prepare", "implement", "document"}, deps: map[string][]string{"implement": {"prepare"}, "document": {"prepare"}}, want: []string{"prepare:worker", "implement:worker", "document:worker"}},
		{name: "fan in", ids: []string{"api", "ui", "release"}, deps: map[string][]string{"release": {"api", "ui"}}, want: []string{"api:worker", "ui:worker", "release:worker"}},
		{name: "multiple roots", ids: []string{"research", "scaffold", "integrate"}, deps: map[string][]string{"integrate": {"research", "scaffold"}}, want: []string{"research:worker", "scaffold:worker", "integrate:worker"}},
		{name: "deterministic ready ordering", ids: []string{"blocked", "first", "second"}, deps: map[string][]string{"blocked": {"second"}}, want: []string{"first:worker", "second:worker", "blocked:worker"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			p := &schedulingProvider{}
			e := newSchedulingEngine(t, schedulingWorkflow(repo, tt.name, tt.ids, tt.deps, "true"), p)
			if err := e.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(p.calls, ","); got != strings.Join(tt.want, ",") {
				t.Fatalf("actor calls = %q, want %q", got, strings.Join(tt.want, ","))
			}
			assertSchedulingCompletion(t, e)
		})
	}
}

func TestV1Alpha2SchedulerFailureStopsDependents(t *testing.T) {
	t.Run("actor failure", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{action: func(context.Context, provider.Request) error { return errors.New("actor stopped") }}
		e := newSchedulingEngine(t, schedulingWorkflow(repo, "actor-failure", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "true"), p)
		if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "actor stopped") {
			t.Fatalf("run error = %v", err)
		}
		assertSchedulingCalls(t, p, "root:worker")
		assertNoPhaseMarker(t, e, "child")
	})

	t.Run("validation failure does not accept actor output", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		e := newSchedulingEngine(t, schedulingWorkflow(repo, "validation-failure", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "false"), p)
		if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "validation") {
			t.Fatalf("run error = %v", err)
		}
		assertSchedulingCalls(t, p, "root:worker")
		assertNoPhaseMarker(t, e, "root")
		assertNoPhaseMarker(t, e, "child")
	})

	t.Run("repair exhaustion", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		w := schedulingWorkflow(repo, "repair-exhaustion", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "false")
		gate := w.Spec.Validation["gate"]
		gate.OnFailure = workflow.FailurePolicy{Strategy: "repair-once", MaxRepairAttempts: 1, Repair: workflow.Repair{Actor: "repair", Prompt: "repair"}}
		w.Spec.Validation["gate"] = gate
		e := newSchedulingEngine(t, w, p)
		if err := e.Run(context.Background()); err == nil {
			t.Fatal("first failed validation unexpectedly succeeded")
		}
		assertSchedulingCalls(t, p, "root:worker", "root:repair")
		if err := newSchedulingEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("restart error = %v", err)
		}
		assertSchedulingCalls(t, p, "root:worker", "root:repair")
	})
}

func TestV1Alpha2SchedulerResumesOnlyDurablePhaseEvidence(t *testing.T) {
	t.Run("already accepted dependency is not replayed", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		w := schedulingWorkflow(repo, "already-accepted", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "true")
		e := newSchedulingEngine(t, w, p)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		if err := e.runPhase(context.Background(), "root"); err != nil {
			t.Fatal(err)
		}
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker", "child:worker")
	})

	t.Run("interruption during actor execution reruns only that actor", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{action: func(ctx context.Context, _ provider.Request) error { return ctx.Err() }}
		w := schedulingWorkflow(repo, "interrupt-actor", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "true")
		e := newSchedulingEngine(t, w, p)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := e.Run(ctx); err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted run error = %v", err)
		}
		p.action = nil
		if err := newSchedulingEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker", "root:worker", "child:worker")
	})

	t.Run("interruption after actor completion resumes acceptance without replay", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		w := schedulingWorkflow(repo, "interrupt-after-actor", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "true")
		e := newSchedulingEngine(t, w, p)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		active, err := e.newActivePhase("root")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
			t.Fatal(err)
		}
		root, err := e.phaseByID("root")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.runPhaseActor(context.Background(), root, root.Prompt, &active); err != nil {
			t.Fatal(err)
		}
		if err := newSchedulingEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker", "child:worker")
	})

	t.Run("interruption during validation resumes validation without replay", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		command := "if test -f validation.allow; then true; else touch validation.started; while :; do sleep 0.01; done; fi"
		w := schedulingWorkflow(repo, "interrupt-validation", []string{"root", "child"}, map[string][]string{"child": {"root"}}, command)
		e := newSchedulingEngine(t, w, p)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- e.Run(ctx) }()
		waitForFile(t, filepath.Join(repo, "validation.started"))
		cancel()
		if err := <-done; err == nil {
			t.Fatalf("interrupted validation error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "validation.allow"), []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := newSchedulingEngine(t, w, p).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker", "child:worker")
	})

	t.Run("restart derives the same next ready phase", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		w := schedulingWorkflow(repo, "restart-ready", []string{"root", "first", "second"}, map[string][]string{"first": {"root"}, "second": {"root"}}, "true")
		e := newSchedulingEngine(t, w, p)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		if err := e.runPhase(context.Background(), "root"); err != nil {
			t.Fatal(err)
		}
		next, err := NewReadyNodeScheduler(w.DependencyGraph).Next(e.phaseDependencyAccepted)
		if err != nil || next == nil || next.ID != "first" {
			t.Fatalf("next before restart = %#v, err = %v", next, err)
		}
		restarted := newSchedulingEngine(t, w, p)
		next, err = NewReadyNodeScheduler(w.DependencyGraph).Next(restarted.phaseDependencyAccepted)
		if err != nil || next == nil || next.ID != "first" {
			t.Fatalf("next after restart = %#v, err = %v", next, err)
		}
		if err := restarted.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker", "first:worker", "second:worker")
	})
}

func schedulingWorkflow(repo, name string, ids []string, dependencies map[string][]string, gateCommand string) *workflow.Workflow {
	graph := workflow.PhaseDependencyGraph{}
	phases := make([]workflow.Phase, 0, len(ids))
	for index, id := range ids {
		graph.Nodes = append(graph.Nodes, workflow.PhaseDependencyNode{ID: id, AuthoredOrder: index})
		for _, dependency := range dependencies[id] {
			graph.Edges = append(graph.Edges, workflow.PhaseDependencyEdge{Phase: id, DependsOn: dependency, SatisfiedWhen: workflow.PhaseDependencyAccepted})
		}
		phases = append(phases, workflow.Phase{ID: id, Kind: "implementation", Label: id, Actor: "worker", Prompt: id, Validation: "gate", RequiresChange: true})
	}
	return &workflow.Workflow{
		APIVersion:      "agentflow.dev/v1alpha2",
		Kind:            "AgentWorkflow",
		Metadata:        workflow.Metadata{Name: name},
		DependencyGraph: graph,
		Spec: workflow.Spec{
			State: workflow.StateSpec{
				Initialize: workflow.StateInitialize{RequireCleanImplementationWorkspace: true, RequireNamedBranch: true},
				Resume:     workflow.StateResume{Enabled: boolPtr(true), RequireBaseIsAncestorOfHead: true, RequireSameBranch: true},
				Lineage:    workflow.StateLineage{RequireBaseCommitExists: true, RequireBaseIsAncestorOfHead: true, RequireSameNamedBranch: true},
			},
			Workspace: workflow.WorkspaceSpec{Root: repo, MutationPolicy: workflow.MutationPolicy{Allowed: []string{"*"}}, Checkpointing: workflow.CheckpointSpec{CommitMessage: "checkpoint: {{ phase.label }}"}},
			Agents: map[string]workflow.Agent{
				"worker": {Runner: "test", MayCommit: true},
				"repair": {Runner: "test", MayCommit: true},
			},
			Tools:      map[string]workflow.Tool{"gate": {Type: "shell", Command: gateCommand}},
			Validation: map[string]workflow.Validation{"gate": {Steps: []workflow.ToolUse{{Uses: "gate"}}}},
			Lifecycle:  workflow.LifecyclePolicy{Policy: "safe-resume"},
			Phases:     phases,
			Completion: map[string]workflow.Completion{"default": {FinalValidation: "gate"}},
		},
	}
}

func newSchedulingEngine(t *testing.T, w *workflow.Workflow, p *schedulingProvider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func assertSchedulingCalls(t *testing.T, p *schedulingProvider, want ...string) {
	t.Helper()
	if got := strings.Join(p.calls, ","); got != strings.Join(want, ",") {
		t.Fatalf("actor calls = %q, want %q", got, strings.Join(want, ","))
	}
}

func assertNoPhaseMarker(t *testing.T, e *Engine, phaseID string) {
	t.Helper()
	p, err := e.phaseByID(phaseID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil || ok {
		t.Fatalf("phase %q marker accepted: ok=%v err=%v", phaseID, ok, err)
	}
}

func assertSchedulingCompletion(t *testing.T, e *Engine) {
	t.Helper()
	if ok, _, err := e.validCommitMarker(e.workflowCompleteMarker()); err != nil || !ok {
		t.Fatalf("completion marker: ok=%v err=%v", ok, err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
