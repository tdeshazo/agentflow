package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestV1Alpha2ParallelSchedulerRecomputesResumedActorNetChange(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-resume-net-change", []string{"left", "right"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}

	var leftCalls atomic.Int32
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		phase := request.Metadata["phase"]
		path := filepath.Join(request.Workspace, phase, "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if phase != "left" {
			return os.WriteFile(path, []byte("right\n"), 0o644)
		}
		if leftCalls.Add(1) == 1 {
			if err := os.WriteFile(path, []byte("partial\n"), 0o644); err != nil {
				return err
			}
			return errors.New("simulated interrupted left actor")
		}
		return os.Remove(path)
	}}

	first := newSchedulingEngine(t, w, p)
	if err := first.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "simulated interrupted left actor") {
		t.Fatalf("first run error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "left", "result.txt")); err != nil {
		t.Fatalf("partial actor work was not retained: %v", err)
	}

	restarted := newSchedulingEngine(t, w, p)
	err := restarted.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "produced no net repository change") {
		t.Fatalf("resumed run error = %v, want no net repository change", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "left", "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("resumed actor did not revert partial work: %v", err)
	}
	assertNoPhaseMarker(t, restarted, "left")

	var active ActivePhase
	if ok, stateErr := restarted.Store.GetJSON(restarted.activeRecord(), &active); stateErr != nil || !ok {
		t.Fatalf("active recovery state: present=%t err=%v", ok, stateErr)
	}
	if len(active.ActorChangedPaths) != 0 {
		t.Fatalf("resumed actor net changed paths = %v, want none", active.ActorChangedPaths)
	}
}

func TestParallelNetChangeReplacementSurvivesLifecyclePersistence(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-net-change-persistence", []string{"left", "right"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}

	e := newSchedulingEngine(t, w, &schedulingProvider{skipPhaseFile: true})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("left")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	active.ParallelBatch = "test-batch"
	active.ActorChangedPaths = []string{"left/stale.txt"}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "left", "replacement.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	actions := []workflow.PhaseAction{
		{AssertNetRepositoryChangeSincePhaseStart: true},
		{PersistActivePhase: workflow.PersistActivePhase{Fields: []string{"actor_changed_paths"}}},
	}
	if err := e.runPhaseActions(context.Background(), phase, &active, actions); err != nil {
		t.Fatal(err)
	}
	want := []string{"left/replacement.txt"}
	if !slices.Equal(active.ActorChangedPaths, want) {
		t.Fatalf("in-memory actor changed paths = %v, want %v", active.ActorChangedPaths, want)
	}
	var persisted ActivePhase
	if ok, stateErr := e.Store.GetJSON(e.activeRecord(), &persisted); stateErr != nil || !ok {
		t.Fatalf("persisted active state: present=%t err=%v", ok, stateErr)
	}
	if !slices.Equal(persisted.ActorChangedPaths, want) {
		t.Fatalf("persisted actor changed paths = %v, want %v", persisted.ActorChangedPaths, want)
	}
}
