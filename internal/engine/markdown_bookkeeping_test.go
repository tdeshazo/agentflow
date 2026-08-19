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

type engineOwnedProvider struct {
	calls         int
	progressEdit  string
	progressEdits []string
}

func (p *engineOwnedProvider) Name() string { return "engine-owned-test" }

func (p *engineOwnedProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.calls++
	if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("implemented\n"), 0o644); err != nil {
		return provider.Result{}, err
	}
	edits := p.progressEdits
	if len(edits) == 0 && p.progressEdit != "" {
		edits = []string{p.progressEdit}
	}
	for _, edit := range edits {
		path := filepath.Join(request.Workspace, "progress.md")
		b, err := os.ReadFile(path)
		if err != nil {
			return provider.Result{}, err
		}
		updated := strings.Replace(string(b), "- [ ] "+edit, "- [x] "+edit, 1)
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return provider.Result{}, err
		}
	}
	return provider.Result{}, nil
}

func TestEngineOwnedProgressAdvancesOnlyDeclaredCriterion(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	p := &engineOwnedProvider{}
	e := newEngineOwnedEngine(t, repo, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
	progress, err := os.ReadFile(filepath.Join(repo, "progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(progress), "- [x] first\n- [ ] second\n"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
	if _, ok, err := e.Store.Resolve("phases/one"); err != nil || !ok {
		t.Fatalf("phase marker: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("worktree is dirty: %q", got)
	}
}

func TestEngineOwnedProgressRejectsActorControlledOrExtraProgress(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edits []string
	}{
		{name: "actor closes target", edits: []string{"first"}},
		{name: "actor closes a different criterion", edits: []string{"second"}},
		{name: "actor manufactures extra progress", edits: []string{"first", "second"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newEngineOwnedRepo(t)
			p := &engineOwnedProvider{progressEdits: tc.edits}
			e := newEngineOwnedEngine(t, repo, p)
			err := e.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "actor changed progress") {
				t.Fatalf("run error = %v, want actor progress rejection", err)
			}
			if _, ok, markerErr := e.Store.Resolve("phases/one"); markerErr != nil || ok {
				t.Fatalf("phase marker after rejected progress: ok=%v err=%v", ok, markerErr)
			}
		})
	}
}

func TestMarkdownBookkeepingPreservesContentAndFailsClosed(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	roadmap := "# Roadmap\n\nStatus: In Progress  \n\n- [ ] unrelated\n"
	index := "# Index\n\n5. [ ] [Complete Combat Workflow]\n"
	if err := os.WriteFile(filepath.Join(repo, "roadmap.md"), []byte(roadmap), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "roadmap.md", "index.md")
	gitIn(t, repo, "commit", "-qm", "add bookkeeping files")

	w := engineOwnedWorkflow(repo)
	w.Spec.Phases = []workflow.Phase{{
		ID:    "close",
		Kind:  "bookkeeping",
		Label: "close roadmap",
		Bookkeeping: []workflow.MarkdownTransition{
			{Type: "markdown-status", Path: "roadmap.md", Label: "Status", From: "In Progress", To: "Complete"},
			{Type: "markdown-index", Path: "index.md", Item: "[Complete Combat Workflow]", State: "checked"},
		},
	}}
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"roadmap.md", "index.md"}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "close"}, {Complete: "done"}}
	p := &engineOwnedProvider{}
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("engine-owned bookkeeping invoked actor %d times", p.calls)
	}
	gotRoadmap, err := os.ReadFile(filepath.Join(repo, "roadmap.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Roadmap\n\nStatus: Complete  \n\n- [ ] unrelated\n"; string(gotRoadmap) != want {
		t.Fatalf("roadmap = %q, want %q", gotRoadmap, want)
	}
	gotIndex, err := os.ReadFile(filepath.Join(repo, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Index\n\n5. [x] [Complete Combat Workflow]\n"; string(gotIndex) != want {
		t.Fatalf("index = %q, want %q", gotIndex, want)
	}

	duplicate := filepath.Join(repo, "duplicate.md")
	if err := os.WriteFile(duplicate, []byte("- [ ] same\n- [ ] same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.mutateMarkdownChecklist("duplicate.md", "same", "checked"); err == nil || !strings.Contains(err.Error(), "matched 2") {
		t.Fatalf("duplicate mutation error = %v", err)
	}
	if err := e.mutateMarkdownChecklist("../outside.md", "same", "checked"); err == nil || !strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("escaping mutation error = %v", err)
	}
}

func TestBookkeepingRecoveryFinishesDurablePendingTransition(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "roadmap.md"), []byte("Status: In Progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "roadmap.md")
	gitIn(t, repo, "commit", "-qm", "add roadmap")
	w := engineOwnedWorkflow(repo)
	w.Spec.Phases = []workflow.Phase{{
		ID: "close", Kind: "bookkeeping", Label: "close roadmap",
		Bookkeeping: []workflow.MarkdownTransition{{Type: "markdown-status", Path: "roadmap.md", Label: "Status", From: "In Progress", To: "Complete"}},
	}}
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"roadmap.md"}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "close"}, {Complete: "done"}}
	e, err := New(w, map[string]provider.Provider{"test": &engineOwnedProvider{}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhase("close")
	if err != nil {
		t.Fatal(err)
	}
	active.ActorCompleted = true
	active.BookkeepingPending = true
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if _, err := e.transitionMarkdownStatus("roadmap.md", "Status", "In Progress", "Complete"); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.Store.Resolve("phases/close"); err != nil || !ok {
		t.Fatalf("recovered bookkeeping marker: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("recovered bookkeeping worktree is dirty: %q", got)
	}
}

func TestBookkeepingRecoveryRejectsSameFileExternalEdit(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "roadmap.md"), []byte("Status: In Progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "roadmap.md")
	gitIn(t, repo, "commit", "-qm", "add roadmap")
	w := engineOwnedWorkflow(repo)
	w.Spec.Phases = []workflow.Phase{{
		ID: "close", Kind: "bookkeeping", Label: "close roadmap",
		Bookkeeping: []workflow.MarkdownTransition{{Type: "markdown-status", Path: "roadmap.md", Label: "Status", From: "In Progress", To: "Complete"}},
	}}
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"roadmap.md"}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "close"}}
	e, err := New(w, map[string]provider.Provider{"test": &engineOwnedProvider{}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhase("close")
	if err != nil {
		t.Fatal(err)
	}
	active.ActorCompleted = true
	active.BookkeepingPending = true
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if _, err := e.transitionMarkdownStatus("roadmap.md", "Status", "In Progress", "Complete"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "roadmap.md"), []byte("Status: Complete\nUnexpected: external edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "changed outside its declared transitions") {
		t.Fatalf("recovery error = %v, want external bookkeeping edit rejection", err)
	}
	if _, ok, markerErr := e.Store.Resolve("phases/close"); markerErr != nil || ok {
		t.Fatalf("bookkeeping marker after external edit: ok=%v err=%v", ok, markerErr)
	}
}

func TestBookkeepingRejectsMutationOutsideItsDeclaredBoundary(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "roadmap.md"), []byte("Status: In Progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "roadmap.md")
	gitIn(t, repo, "commit", "-qm", "add roadmap")
	w := engineOwnedWorkflow(repo)
	w.Spec.Phases = []workflow.Phase{{
		ID: "close", Kind: "bookkeeping", Label: "close roadmap",
		Bookkeeping: []workflow.MarkdownTransition{{Type: "markdown-status", Path: "roadmap.md", Label: "Status", From: "In Progress", To: "Complete"}},
	}}
	// The transition path is intentionally omitted from the workspace boundary.
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"index.md"}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "close"}}
	e, err := New(w, map[string]provider.Provider{"test": &engineOwnedProvider{}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "out-of-scope file changed: roadmap.md") {
		t.Fatalf("run error = %v, want bookkeeping boundary failure", err)
	}
	if _, ok, markerErr := e.Store.Resolve("phases/close"); markerErr != nil || ok {
		t.Fatalf("bookkeeping phase marker after boundary failure: ok=%v err=%v", ok, markerErr)
	}
}

func TestEngineOwnedProgressRecoveryFinishesPendingTransition(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	p := &engineOwnedProvider{}
	e := newEngineOwnedEngine(t, repo, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhase("one")
	if err != nil {
		t.Fatal(err)
	}
	active.ActorCompleted = true
	active.ProgressAdvancePending = true
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := e.mutateMarkdownChecklist("progress.md", "first", "checked"); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 0 {
		t.Fatalf("recovery reran actor %d times", p.calls)
	}
	progress, err := os.ReadFile(filepath.Join(repo, "progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(progress), "- [x] first\n- [ ] second\n"; got != want {
		t.Fatalf("recovered progress = %q, want %q", got, want)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("recovered progress worktree is dirty: %q", got)
	}
}

func newEngineOwnedRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	for name, content := range map[string]string{
		"progress.md": "- [ ] first\n- [ ] second\n",
		"README.md":   "seed\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", "progress.md", "README.md")
	gitIn(t, repo, "commit", "-qm", "seed")
	return repo
}

func newEngineOwnedEngine(t *testing.T, repo string, p provider.Provider) *Engine {
	t.Helper()
	e, err := New(engineOwnedWorkflow(repo), map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func engineOwnedWorkflow(repo string) *workflow.Workflow {
	return &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha1",
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: "engine-owned-transitions"},
		Spec: workflow.Spec{
			Parameters: map[string]workflow.Parameter{"repo_root": {Type: "path", Default: repo}},
			Workspace: workflow.WorkspaceSpec{
				Root:           "{{ parameters.repo_root }}",
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt", "progress.md"}},
				Checkpointing:  workflow.CheckpointSpec{CommitMessage: "test: {{ phase.label }}"},
			},
			Agents: map[string]workflow.Agent{"worker": {Runner: "test"}},
			Tools:  map[string]workflow.Tool{"scope": {Type: "workspace-policy"}},
			Progress: workflow.ProgressSpec{
				Source:    workflow.ProgressSource{Type: "markdown-checklist", Path: "progress.md", UncheckedPattern: `^- \[ \] (.+)$`, CheckedPattern: `^- \[[xX]\] (.+)$`},
				Criteria:  []workflow.Criterion{{ID: "first", Text: "first"}, {ID: "second", Text: "second"}},
				Invariant: workflow.ProgressInvariant{TargetedMustBeChecked: true, UncheckedCountDelta: -1, NoOtherMayClose: true},
			},
			Validation: map[string]workflow.Validation{"gate": {Steps: []workflow.ToolUse{{Uses: "scope"}}}},
			Lifecycle:  workflow.LifecyclePolicy{Policy: "safe-resume", Validation: "gate"},
			Phases: []workflow.Phase{{
				ID: "one", Kind: "criterion", Label: "first", Actor: "worker", CriterionID: "first", AdvanceProgress: true, RequiresChange: true, Prompt: "implement first",
			}},
			Flow:       []workflow.FlowStep{{Phase: "one"}, {Complete: "done"}},
			Completion: map[string]workflow.Completion{"done": {}},
		},
	}
}
