package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type schedulingProvider struct {
	mu            sync.Mutex
	calls         []string
	contexts      []provider.InvocationContext
	action        func(context.Context, provider.Request) error
	skipPhaseFile bool
}

func (p *schedulingProvider) Name() string                     { return "scheduler-test" }
func (p *schedulingProvider) EnforcesFilesystemBoundary() bool { return true }

func (p *schedulingProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	phase := request.Metadata["phase"]
	p.mu.Lock()
	p.calls = append(p.calls, phase+":"+request.Metadata["actor"])
	p.contexts = append(p.contexts, request.Context)
	p.mu.Unlock()
	if p.action != nil {
		if err := p.action(ctx, request); err != nil {
			return provider.Result{}, err
		}
	}
	if phase == "" {
		return provider.Result{}, nil
	}
	if p.skipPhaseFile {
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
			if got := strings.Join(p.recordedCalls(), ","); got != strings.Join(tt.want, ",") {
				t.Fatalf("actor calls = %q, want %q", got, strings.Join(tt.want, ","))
			}
			assertSchedulingCompletion(t, e)
		})
	}
}

func TestV1Alpha2ParallelSchedulerRunsDisjointFanOutAndJoinsDeterministically(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-disjoint-fan-out", []string{"left", "right", "join"}, map[string][]string{
		"join": {"left", "right"},
	}, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**", "join/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}
	w.Spec.Phases[2].Writes = []string{"join/**"}

	started := make(chan string, 2)
	release := make(chan struct{})
	var concurrent atomic.Int32
	var maximum atomic.Int32
	p := &schedulingProvider{skipPhaseFile: true, action: func(ctx context.Context, request provider.Request) error {
		phase := request.Metadata["phase"]
		if phase == "left" || phase == "right" {
			current := concurrent.Add(1)
			for {
				observed := maximum.Load()
				if current <= observed || maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			started <- phase
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			concurrent.Add(-1)
		}
		path := filepath.Join(request.Workspace, phase, "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(phase+"\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, p)
	done := make(chan error, 1)
	go func() { done <- e.Run(context.Background()) }()
	seen := map[string]bool{}
	for range 2 {
		select {
		case phase := <-started:
			seen[phase] = true
		case <-time.After(10 * time.Second):
			t.Fatal("parallel fan-out did not start both actors")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !seen["left"] || !seen["right"] || maximum.Load() != 2 {
		t.Fatalf("parallel starts = %#v, maximum concurrency = %d", seen, maximum.Load())
	}
	calls := p.recordedCalls()
	if len(calls) != 3 || calls[2] != "join:worker" {
		t.Fatalf("actor calls = %v, want two fan-out calls followed by join", calls)
	}
	assertSchedulingCompletion(t, e)
}

func TestV1Alpha2ParallelSchedulerSerializesOverlappingWrites(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-overlap-serialization", []string{"first", "second"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"shared/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	for i := range w.Spec.Phases {
		w.Spec.Phases[i].Writes = []string{"shared/**"}
	}
	var concurrent atomic.Int32
	var maximum atomic.Int32
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		current := concurrent.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		concurrent.Add(-1)
		path := filepath.Join(request.Workspace, "shared", request.Metadata["phase"]+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("done\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrency = %d, want overlapping writers serialized", maximum.Load())
	}
	assertSchedulingCalls(t, p, "first:worker", "second:worker")
}

func TestV1Alpha2ParallelSchedulerRecoversDurableBatch(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-durable-recovery", []string{"left", "right", "join"}, map[string][]string{
		"join": {"left", "right"},
	}, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**", "join/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}
	w.Spec.Phases[2].Writes = []string{"join/**"}

	var failLeft atomic.Bool
	failLeft.Store(true)
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		phase := request.Metadata["phase"]
		if phase == "left" && failLeft.CompareAndSwap(true, false) {
			return errors.New("simulated parallel actor interruption")
		}
		path := filepath.Join(request.Workspace, phase, "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(phase+"\n"), 0o644); err != nil {
			return err
		}
		return nil
	}}
	first := newSchedulingEngine(t, w, p)
	if err := first.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "simulated parallel actor interruption") {
		t.Fatalf("first run error = %v", err)
	}
	if _, ok, err := first.Store.Resolve(parallelBatchRecord); err != nil || !ok {
		t.Fatalf("durable parallel batch: present=%t err=%v", ok, err)
	}
	status, err := first.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.ParallelPhases) != 2 || status.ParallelPhases[0] != "left" || status.ParallelPhases[1] != "right" {
		t.Fatalf("parallel status = %#v", status.ParallelPhases)
	}

	restarted := newSchedulingEngine(t, w, p)
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := restarted.Store.Resolve(parallelBatchRecord); err != nil || ok {
		t.Fatalf("parallel batch after recovery: present=%t err=%v", ok, err)
	}
	assertSchedulingCompletion(t, restarted)
	calls := p.recordedCalls()
	if calls[len(calls)-1] != "join:worker" {
		t.Fatalf("actor calls = %v, want fan-in after recovered branches", calls)
	}
}

func TestV1Alpha2ParallelSchedulerCancelsSiblingActorsAfterFailure(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-cancellation", []string{"fail", "sibling"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"fail/**", "sibling/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"fail/**"}
	w.Spec.Phases[1].Writes = []string{"sibling/**"}

	started := make(chan struct{}, 2)
	bothStarted := make(chan struct{})
	var startCount atomic.Int32
	var canceled atomic.Bool
	p := &schedulingProvider{skipPhaseFile: true, action: func(ctx context.Context, request provider.Request) error {
		started <- struct{}{}
		if startCount.Add(1) == 2 {
			close(bothStarted)
		}
		select {
		case <-bothStarted:
		case <-ctx.Done():
			return ctx.Err()
		}
		if request.Metadata["phase"] == "fail" {
			return errors.New("parallel branch failed")
		}
		<-ctx.Done()
		canceled.Store(true)
		return ctx.Err()
	}}
	e := newSchedulingEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "parallel branch failed") {
		t.Fatalf("run error = %v", err)
	}
	if !canceled.Load() {
		t.Fatal("sibling actor did not observe scheduler cancellation")
	}
	if err := e.Reset(); err != nil {
		t.Fatalf("reset interrupted parallel batch: %v", err)
	}
}

func TestV1Alpha2ParallelSchedulerReturnsLaterFailureInsteadOfEarlierSiblingCancellation(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-cancellation-cause", []string{"sibling", "fail"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"sibling/**", "fail/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"sibling/**"}
	w.Spec.Phases[1].Writes = []string{"fail/**"}

	bothStarted := make(chan struct{})
	var startCount atomic.Int32
	p := &schedulingProvider{skipPhaseFile: true, action: func(ctx context.Context, request provider.Request) error {
		if startCount.Add(1) == 2 {
			close(bothStarted)
		}
		select {
		case <-bothStarted:
		case <-ctx.Done():
			return ctx.Err()
		}
		if request.Metadata["phase"] == "fail" {
			return errors.New("later parallel branch failed")
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	e := newSchedulingEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "later parallel branch failed") {
		t.Fatalf("run error = %v, want later branch failure", err)
	}
	if err := e.Reset(); err != nil {
		t.Fatalf("reset interrupted parallel batch: %v", err)
	}
}

func TestV1Alpha2ParallelSchedulerEnforcesDeclaredWriteScope(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-write-scope", []string{"left", "right"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		phase := request.Metadata["phase"]
		directory := phase
		if phase == "left" {
			directory = "right"
		}
		path := filepath.Join(request.Workspace, directory, phase+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("done\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, p)
	err := e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "phase left changed path outside its write scope") {
		t.Fatalf("run error = %v", err)
	}
	assertNoPhaseMarker(t, e, "left")
	assertNoPhaseMarker(t, e, "right")
	if err := e.Reset(); err != nil {
		t.Fatalf("reset safety-failed parallel batch: %v", err)
	}
}

func TestV1Alpha2InlineActorSchedulerPreservesAcceptanceBoundary(t *testing.T) {
	tests := []struct {
		name        string
		validation  string
		wantCalls   []string
		wantFailure bool
	}{
		{
			name:       "accepted dependency schedules inline actor",
			validation: "true",
			wantCalls:  []string{"root:worker", "child:__inline_actor__child"},
		},
		{
			name:        "actor completion without validation blocks inline dependent",
			validation:  "false",
			wantCalls:   []string{"root:worker"},
			wantFailure: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			p := &schedulingProvider{}
			w := inlineActorSchedulingWorkflow(t, tt.name, tt.validation)
			e := newSchedulingEngineAt(t, w, p, repo)
			err := e.Run(context.Background())
			if tt.wantFailure {
				if err == nil || !strings.Contains(err.Error(), "validation") {
					t.Fatalf("run error = %v", err)
				}
				assertNoPhaseMarker(t, e, "root")
				assertNoPhaseMarker(t, e, "child")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertSchedulingCompletion(t, e)
			}
			assertSchedulingCalls(t, p, tt.wantCalls...)
		})
	}
}

func TestV1Alpha2ActorCreatedCommitDoesNotSatisfyValidationOrDependency(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "root" {
			return nil
		}
		if err := os.WriteFile(filepath.Join(request.Workspace, "root.txt"), []byte("actor commit\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "root.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "actor-created root commit")
		return nil
	}}
	w := schedulingWorkflow(repo, "actor-commit-not-acceptance", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "false")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model", MayCommit: true}
	e := newSchedulingEngine(t, w, p)
	err := e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "validation") {
		t.Fatalf("actor-created commit bypass error = %v", err)
	}
	assertNoPhaseMarker(t, e, "root")
	assertNoPhaseMarker(t, e, "child")
	assertSchedulingCalls(t, p, "root:worker")
}

func TestV1Alpha2WorkspacePolicyValidationRuns(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{}
	w := schedulingWorkflow(repo, "workspace-policy-validation", []string{"root"}, nil, "true")
	w.Spec.Tools["gate"] = workflow.Tool{Type: "workspace-policy"}
	e := newSchedulingEngine(t, w, p)

	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSchedulingCalls(t, p, "root:worker")
	assertSchedulingCompletion(t, e)
}

func TestV1Alpha2SchedulerFailureStopsDependents(t *testing.T) {
	t.Run("invalid references fail before any actor runs", func(t *testing.T) {
		repo := newDurableRepo(t)
		p := &schedulingProvider{}
		w := schedulingWorkflow(repo, "invalid-reference-preflight", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "true")
		w.Spec.Phases[1].Actor = "missing"
		e := newSchedulingEngine(t, w, p)
		if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), `phase "child" references unknown actor "missing"`) {
			t.Fatalf("run error = %v", err)
		}
		assertSchedulingCalls(t, p)
	})

	for _, tt := range []struct {
		name    string
		tool    workflow.Tool
		wantErr string
	}{
		{
			name:    "empty validation command fails before any actor runs",
			tool:    workflow.Tool{Type: "shell", Command: " \t\n"},
			wantErr: `validation "gate" references shell tool "gate" with an empty command`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			p := &schedulingProvider{}
			w := schedulingWorkflow(repo, tt.name, []string{"root"}, nil, "true")
			w.Spec.Tools["gate"] = tt.tool
			e := newSchedulingEngine(t, w, p)
			if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run error = %v, want error containing %q", err, tt.wantErr)
			}
			assertSchedulingCalls(t, p)
		})
	}

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

func TestV1Alpha2CompletionIsASeparateDurableTransition(t *testing.T) {
	t.Run("phase validation does not satisfy the final validation", func(t *testing.T) {
		repo := newDurableRepo(t)
		countFile := filepath.Join(t.TempDir(), "validation-count")
		w := conciseCompletionWorkflow(t, repo, "distinct-final-validation", fmt.Sprintf("printf x >> %s; test -f root.txt", countFile))
		if len(w.Spec.Flow) != 0 {
			t.Fatalf("concise v1alpha2 workflow unexpectedly has flow: %#v", w.Spec.Flow)
		}
		p := &schedulingProvider{}
		e := newSchedulingEngineAt(t, w, p, repo)
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(mustReadFile(t, countFile)); got != 2 {
			t.Fatalf("validation executions = %d, want phase and distinct final validation", got)
		}
		assertSchedulingCalls(t, p, "root:worker")
		assertSchedulingCompletion(t, e)
	})

	t.Run("failed final validation does not complete and resumes without replaying phases", func(t *testing.T) {
		repo := newDurableRepo(t)
		allow := filepath.Join(t.TempDir(), "validation.allow")
		if err := os.WriteFile(allow, []byte("allow\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w := conciseCompletionWorkflow(t, repo, "final-validation-failure", fmt.Sprintf("test -f root.txt && test -f %s", allow))
		p := &schedulingProvider{}
		e := newSchedulingEngineAt(t, w, p, repo)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		if err := e.runPhase(context.Background(), "root"); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(allow); err != nil {
			t.Fatal(err)
		}
		if err := e.runV1Alpha2Schedule(context.Background()); err == nil || !strings.Contains(err.Error(), "validation tests failed") {
			t.Fatalf("final validation error = %v", err)
		}
		assertNoSchedulingCompletion(t, e)
		assertSchedulingCalls(t, p, "root:worker")

		if err := os.WriteFile(allow, []byte("allow\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := newSchedulingEngineAt(t, w, p, repo).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertSchedulingCalls(t, p, "root:worker")
		assertSchedulingCompletion(t, e)
	})

	t.Run("restart reuses only durable final evidence and persisted completion", func(t *testing.T) {
		repo := newDurableRepo(t)
		countFile := filepath.Join(t.TempDir(), "validation-count")
		w := conciseCompletionWorkflow(t, repo, "restart-final-validation", fmt.Sprintf("printf x >> %s; test -f root.txt", countFile))
		p := &schedulingProvider{}
		e := newSchedulingEngineAt(t, w, p, repo)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		if err := e.runPhase(context.Background(), "root"); err != nil {
			t.Fatal(err)
		}
		if err := e.runCompletionValidation(context.Background(), "default", "tests"); err != nil {
			t.Fatal(err)
		}
		assertNoSchedulingCompletion(t, e)
		if got := len(mustReadFile(t, countFile)); got != 2 {
			t.Fatalf("validation executions before restart = %d, want phase and final validation", got)
		}

		if err := newSchedulingEngineAt(t, w, p, repo).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(mustReadFile(t, countFile)); got != 2 {
			t.Fatalf("restart reran final validation despite durable evidence: %d", got)
		}
		assertSchedulingCalls(t, p, "root:worker")
		assertSchedulingCompletion(t, e)

		if err := newSchedulingEngineAt(t, w, p, repo).Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(mustReadFile(t, countFile)); got != 2 {
			t.Fatalf("restart reran a persisted completion: %d", got)
		}
	})
}

func TestV1Alpha2ConformanceExampleExecutesAuthorityBoundaries(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "v1alpha2-concise.yaml")
	d, err := workflow.Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	result := workflow.Validate(d)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("status = %s, normalized = %#v, diagnostics = %#v", result.Status, result.Normalized, result.Diagnostics)
	}
	w := result.Normalized.Workflow
	if got := w.Spec.Phases[1]; got.Kind != "audit" || got.RequiresChange {
		t.Fatalf("review phase = kind %q, requiresChange %t; want audit with no required change", got.Kind, got.RequiresChange)
	}
	toolName := w.Spec.Validation["tests"].Steps[0].Uses
	tool := w.Spec.Tools[toolName]
	validationCount := filepath.Join(t.TempDir(), "validation-count")
	tool.Command = fmt.Sprintf("printf x >> %s; test -f src/repaired.txt", validationCount)
	w.Spec.Tools[toolName] = tool

	accepted := map[string]bool{}
	ready := NewReadyNodeScheduler(w.DependencyGraph)
	next, err := ready.Next(func(id string) (bool, error) { return accepted[id], nil })
	if err != nil || next == nil || next.ID != "implement" {
		t.Fatalf("initial ready phase = %#v, err = %v", next, err)
	}
	accepted["implement"] = true
	next, err = ready.Next(func(id string) (bool, error) { return accepted[id], nil })
	if err != nil || next == nil || next.ID != "review" {
		t.Fatalf("ready phase after accepted implement = %#v, err = %v", next, err)
	}

	coderCalls := 0
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] == "coder" {
			coderCalls++
			if coderCalls == 2 {
				return os.WriteFile(filepath.Join(request.Workspace, "src", "repaired.txt"), []byte("repaired\n"), 0o644)
			}
		}
		return nil
	}}
	e, err := New(w, map[string]provider.Provider{"codex": p}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSchedulingCalls(t, p, "implement:coder", "implement:coder", "review:__inline_actor__review")
	if coderCalls != 2 {
		t.Fatalf("coder calls = %d, want one implementation call plus exactly one repair call", coderCalls)
	}
	if got := len(mustReadFile(t, validationCount)); got != 4 {
		t.Fatalf("validation executions = %d, want initial validation, post-repair rerun, review validation, and final validation", got)
	}
	assertSchedulingCompletion(t, e)

	restarted, err := New(w, map[string]provider.Provider{"codex": p}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	assertSchedulingCompletion(t, restarted)
}

func TestV1Alpha2ResetPolicyDeniesResetBeforeStateMutation(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "reset-denied", []string{"root"}, nil, "true")
	allow := false
	w.Spec.State.Reset.Allowed = &allow

	err := newSchedulingEngineAt(t, w, &schedulingProvider{}, repo).Reset()
	if err == nil || !strings.Contains(err.Error(), "reset is disabled") {
		t.Fatalf("reset error = %v, want explicit reset-policy denial", err)
	}
}

func TestV1Alpha2ScheduleRecordsHumanGateBeforeCompletion(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "human-gated", []string{"root"}, nil, "true")
	w.Spec.HumanGates = []workflow.HumanGate{{
		ID:              "release",
		Requires:        []string{"root"},
		Instructions:    "Verify the release candidate.",
		Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "approve"},
	}}
	e := newSchedulingEngineAt(t, w, &schedulingProvider{}, repo)
	e.In = strings.NewReader("approve\n")
	e.HumanGateInteractive = func(io.Reader, io.Writer) bool { return false }
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.validCommitMarker("human/release"); err != nil || !ok {
		t.Fatalf("human-gate evidence = %t, %v", ok, err)
	}
	assertSchedulingCompletion(t, e)
}

func TestV1Alpha2ScheduleRecordsConditionallySkippedPhaseAsAccepted(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "conditional-skip", []string{"implement", "audit", "follow-up"}, map[string][]string{
		"audit":     {"implement"},
		"follow-up": {"audit"},
	}, "true")
	w.Spec.Parameters = map[string]workflow.Parameter{"run_audit": {Type: "boolean", Default: false}}
	w.Spec.Phases[1].Kind = "audit"
	w.Spec.Phases[1].RequiresChange = false
	w.Spec.Phases[1].If = "{{ parameters.run_audit }}"

	p := &schedulingProvider{}
	e := newSchedulingEngineAt(t, w, p, repo)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSchedulingCalls(t, p, "implement:worker", "follow-up:worker")
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(&w.Spec.Phases[1])); err != nil || !ok {
		t.Fatalf("conditional phase marker = %t, %v", ok, err)
	}
	assertSchedulingCompletion(t, e)
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
				"worker": {Runner: "test", Model: "test-model", MayCommit: true},
				"repair": {Runner: "test", Model: "test-model", MayCommit: true},
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
	return newSchedulingEngineAt(t, w, p, "")
}

func newSchedulingEngineAt(t *testing.T, w *workflow.Workflow, p *schedulingProvider, repo string) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func assertSchedulingCalls(t *testing.T, p *schedulingProvider, want ...string) {
	t.Helper()
	if got := strings.Join(p.recordedCalls(), ","); got != strings.Join(want, ",") {
		t.Fatalf("actor calls = %q, want %q", got, strings.Join(want, ","))
	}
}

func (p *schedulingProvider) recordedCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
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

func assertNoSchedulingCompletion(t *testing.T, e *Engine) {
	t.Helper()
	if ok, _, err := e.validCommitMarker(e.workflowCompleteMarker()); err != nil || ok {
		t.Fatalf("completion marker: ok=%v err=%v", ok, err)
	}
}

func conciseCompletionWorkflow(t *testing.T, repo, name, command string) *workflow.Workflow {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	document := fmt.Sprintf(`
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: %s}
spec:
  workspace: {allowWrites: [root.txt, validation.allow]}
  agents:
    worker: {runner: test, model: test-model}
  validation:
    tests: {run: %q}
  phases:
    - {id: root, actor: worker, prompt: write the root artifact, validation: tests}
  completion: {validation: tests}
`, name, command)
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := workflow.Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	result := workflow.Validate(d)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	return result.Normalized.Workflow
}

func inlineActorSchedulingWorkflow(t *testing.T, name, command string) *workflow.Workflow {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	document := fmt.Sprintf(`
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: %s}
spec:
  workspace: {allowWrites: ["*"]}
  agents:
    worker: {runner: test, model: test-model}
  validation:
    gate: {run: %q}
  phases:
    - {id: root, actor: worker, prompt: create root output, validation: gate}
    - id: child
      actor: {runner: test, model: inline-model}
      prompt: create child output
      validation: gate
      dependsOn: [root]
  completion: {validation: gate}
`, name, command)
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := workflow.Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	result := workflow.Validate(d)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	return result.Normalized.Workflow
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
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
