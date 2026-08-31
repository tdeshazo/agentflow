package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestV1Alpha4WorkItemsAdvanceExactlyOnceAndMirrorMarkdown(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "progress.md"), []byte("- [ ] Add API\n- [ ] Add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "progress.md")
	gitIn(t, repo, "commit", "-qm", "add progress adapter")
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		path := filepath.Join(request.Workspace, "src", request.Metadata["phase"]+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(request.Metadata["phase"]+"\n"), 0o644)
	}}
	engine := newWorkItemEngine(t, repo, providerImpl)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSchedulingCalls(t, providerImpl, "implement-items--add-api:worker", "implement-items--add-tests:worker")
	for _, id := range []string{"add-api", "add-tests"} {
		state, ok, err := engine.workItemState(id)
		if err != nil || !ok || state.Status != "completed" {
			t.Fatalf("work item %q state = %#v, ok=%t, err=%v", id, state, ok, err)
		}
	}
	contents, err := os.ReadFile(filepath.Join(repo, "progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(contents), "- [x] Add API\n- [x] Add tests\n"; got != want {
		t.Fatalf("Markdown adapter = %q, want %q", got, want)
	}
	assertSchedulingCompletion(t, engine)
}

func TestV1Alpha4WorkItemsWithDisjointWritesRunConcurrently(t *testing.T) {
	repo := newDurableRepo(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(ctx context.Context, request provider.Request) error {
		phaseID := request.Metadata["phase"]
		started <- phaseID
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		path := filepath.Join(request.Workspace, "src", phaseID+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(phaseID+"\n"), 0o644)
	}}
	engine := newWorkItemEngine(t, repo, providerImpl)
	engine.Workflow.Spec.Execution.MaxParallel = 2
	engine.Workflow.Spec.Criteria.MarkdownAdapter = nil
	for i := range engine.Workflow.Spec.Phases {
		phase := &engine.Workflow.Spec.Phases[i]
		phase.Writes = []string{"src/" + phase.ID + ".txt"}
	}

	runResult := make(chan error, 1)
	go func() {
		runResult <- engine.Run(context.Background())
	}()
	seen := make(map[string]bool, 2)
	for len(seen) < 2 {
		select {
		case phaseID := <-started:
			seen[phaseID] = true
		case <-time.After(5 * time.Second):
			t.Fatal("work-item actors did not start concurrently")
		}
	}
	close(release)
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	assertSchedulingCompletion(t, engine)
}

func TestV1Alpha4RejectsActorEditedMarkdownAdapterBeforeAdvancement(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "progress.md"), []byte("- [ ] Add API\n- [ ] Add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "progress.md")
	gitIn(t, repo, "commit", "-qm", "add progress adapter")
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "progress.md"), []byte("- [x] Add API\n- [ ] Add tests\n"), 0o644)
	}}
	engine := newWorkItemEngine(t, repo, providerImpl)
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "actor changed progress or bookkeeping file progress.md") {
		t.Fatalf("run error = %v", err)
	}
	if _, ok, err := engine.workItemState("add-api"); err != nil || ok {
		t.Fatalf("actor edit advanced work item: ok=%t err=%v", ok, err)
	}
}

func TestV1Alpha4RequiresChangeFailureDoesNotPublishWorkItemCompletion(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "baseline.txt"), []byte("baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "progress.md"), []byte("- [ ] Add API\n- [ ] Add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "src/baseline.txt", "progress.md")
	gitIn(t, repo, "commit", "-qm", "add work-item baseline")

	providerImpl := &schedulingProvider{skipPhaseFile: true}
	engine := newWorkItemEngine(t, repo, providerImpl)
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "produced no net repository change") {
		t.Fatalf("run error = %v", err)
	}
	if _, ok, stateErr := engine.workItemState("add-api"); stateErr != nil || ok {
		t.Fatalf("requiresChange failure published work-item completion: ok=%t err=%v", ok, stateErr)
	}
	contents, err := os.ReadFile(filepath.Join(repo, "progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(contents), "- [ ] Add API\n- [ ] Add tests\n"; got != want {
		t.Fatalf("Markdown adapter changed before requiresChange acceptance: got %q want %q", got, want)
	}

	// Recovery repeats deterministic acceptance but never reruns the actor or
	// turns its unchanged result into an authoritative work-item completion.
	err = newWorkItemEngine(t, repo, providerImpl).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "produced no net repository change") {
		t.Fatalf("recovery error = %v", err)
	}
	assertSchedulingCalls(t, providerImpl, "implement-items--add-api:worker")
	if _, ok, stateErr := engine.workItemState("add-api"); stateErr != nil || ok {
		t.Fatalf("recovery published work-item completion: ok=%t err=%v", ok, stateErr)
	}
}

func TestV1Alpha4RecoversCompletionPublishedBeforePhaseMarker(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "progress.md"), []byte("- [ ] Add API\n- [ ] Add tests\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "progress.md")
	gitIn(t, repo, "commit", "-qm", "add progress adapter")

	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		path := filepath.Join(request.Workspace, "src", request.Metadata["phase"]+".txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(request.Metadata["phase"]+"\n"), 0o644)
	}}
	interrupted := newWorkItemEngine(t, repo, providerImpl)
	if err := interrupted.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase := &interrupted.Workflow.Spec.Phases[0]
	active, err := interrupted.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	active.ActorCompleted = true
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "api.txt"), []byte("api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Store.SetJSON(interrupted.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.advanceWorkItem(phase, &active); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := interrupted.workItemState("add-api"); err != nil || ok {
		t.Fatalf("prepared work item was published before checkpoint: ok=%t err=%v", ok, err)
	}
	if err := interrupted.checkpoint(phase.Label, phase); err != nil {
		t.Fatal(err)
	}
	active.CheckpointCommit, err = interrupted.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Store.SetJSON(interrupted.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.completeWorkItem(phase, &active); err != nil {
		t.Fatal(err)
	}
	if marked, _, err := interrupted.validCommitMarker(interrupted.phaseMarkerName(phase)); err != nil || marked {
		t.Fatalf("simulated interruption already has phase marker: marked=%t err=%v", marked, err)
	}

	resumed := newWorkItemEngine(t, repo, providerImpl)
	if err := resumed.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSchedulingCalls(t, providerImpl, "implement-items--add-tests:worker")
	state, ok, err := resumed.workItemState("add-api")
	if err != nil || !ok || state.Phase != phase.ID {
		t.Fatalf("recovered work-item state = %#v, ok=%t err=%v", state, ok, err)
	}
	assertSchedulingCompletion(t, resumed)
}

func newWorkItemEngine(t *testing.T, repo string, providerImpl *schedulingProvider) *Engine {
	t.Helper()
	phases := []workflow.Phase{
		{ID: "implement-items--add-api", Kind: "implementation", Label: "implement-items--add-api", Actor: "worker", Prompt: "add api", RequiresChange: true, Validation: "gate", WorkItemID: "add-api", AdvanceWorkItem: true},
		{ID: "implement-items--add-tests", Kind: "implementation", Label: "implement-items--add-tests", Actor: "worker", Prompt: "add tests", RequiresChange: true, Validation: "gate", WorkItemID: "add-tests", AdvanceWorkItem: true},
	}
	w := &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha4", Kind: "AgentWorkflow", Metadata: workflow.Metadata{Name: "typed-work-items"},
		DependencyGraph: workflow.PhaseDependencyGraph{Nodes: []workflow.PhaseDependencyNode{
			{ID: phases[0].ID, AuthoredOrder: 0}, {ID: phases[1].ID, AuthoredOrder: 1},
		}},
		Spec: workflow.Spec{
			Workspace:  workflow.WorkspaceSpec{MutationPolicy: workflow.MutationPolicy{Allowed: []string{"src/**", "progress.md"}}},
			Agents:     map[string]workflow.Agent{"worker": {Runner: "test", Model: "worker", MayCommit: false}},
			Tools:      map[string]workflow.Tool{"gate": {Type: "shell", Command: "test -d src"}},
			Validation: map[string]workflow.Validation{"gate": {Steps: []workflow.ToolUse{{Uses: "gate"}}}},
			Lifecycle:  workflow.LifecyclePolicy{Policy: "safe-resume"},
			Criteria: workflow.CriteriaSpec{
				Items:           []workflow.WorkItem{{ID: "add-api", Description: "Add API"}, {ID: "add-tests", Description: "Add tests"}},
				MarkdownAdapter: &workflow.MarkdownChecklistAdapter{Path: "progress.md", Items: map[string]string{"add-api": "Add API", "add-tests": "Add tests"}},
			},
			Phases: phases, Completion: map[string]workflow.Completion{"default": {FinalValidation: "gate"}},
		},
	}
	engine, err := New(w, map[string]provider.Provider{"test": providerImpl}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
