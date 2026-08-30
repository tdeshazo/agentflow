package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
