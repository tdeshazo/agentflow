package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

// priority5FixtureProvider is deliberately boring: it models an agent that
// edits only the requested fixture file. The runtime, not this provider,
// decides whether the edit is acceptable.
type priority5FixtureProvider struct {
	interruptPhase string
	interrupted    bool
	protectFiles   bool
	calls          int
}

func (p *priority5FixtureProvider) Name() string { return "deterministic-priority5" }

func (p *priority5FixtureProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	p.calls++
	phase := request.Metadata["phase"]
	repo := request.Workspace
	if p.protectFiles {
		return provider.Result{}, os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("tampered\n"), 0o644)
	}
	if request.Metadata["phase_kind"] == "criterion" {
		path := filepath.Join(repo, "docs/planning/roadmap-04-combat-workflow.md")
		b, err := os.ReadFile(path)
		if err != nil {
			return provider.Result{}, err
		}
		criterion := request.Metadata["criterion"]
		old := "- [ ] " + criterion
		updated := strings.Replace(string(b), old, "- [x] "+criterion, 1)
		if updated == string(b) {
			return provider.Result{}, errors.New("target criterion was not unchecked in fixture roadmap")
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return provider.Result{}, err
		}
	}
	if phase == "09" {
		path := filepath.Join(repo, "docs/planning/roadmap-04-combat-workflow.md")
		b, err := os.ReadFile(path)
		if err != nil {
			return provider.Result{}, err
		}
		updated := strings.Replace(string(b), "Status: In Progress", "Status: Complete", 1)
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return provider.Result{}, err
		}
		path = filepath.Join(repo, "docs/planning/README.md")
		b, err = os.ReadFile(path)
		if err != nil {
			return provider.Result{}, err
		}
		updated = strings.Replace(string(b), "5. [ ] [Complete Combat Workflow]", "5. [x] [Complete Combat Workflow]", 1)
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return provider.Result{}, err
		}
	}
	if phase == p.interruptPhase && !p.interrupted {
		p.interrupted = true
		return provider.Result{}, context.Canceled
	}
	return provider.Result{}, ctx.Err()
}

func TestPriority5WorkflowRunsWithDeterministicProviderAndResumes(t *testing.T) {
	repo := newPriority5FixtureRepo(t)
	w := loadPriority5Fixture(t, repo)
	p := &priority5FixtureProvider{interruptPhase: "01"}
	e := newPriority5FixtureEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want cancellation", err)
	}
	if ok, err := e.Store.GetJSON("active-phase", &ActivePhase{}); err != nil || !ok {
		t.Fatalf("active phase after interruption: ok=%v err=%v", ok, err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls before resume = %d, want 1", p.calls)
	}

	if err := newPriority5FixtureEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 9 { // 01 plus 02..09; no replay of accepted 01 work.
		t.Fatalf("provider calls after resume = %d, want 9", p.calls)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve("manual-confirmed"); err != nil || !ok {
		t.Fatalf("human skip evidence: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve("01.done"); err != nil || !ok {
		t.Fatalf("configured phase marker: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("fixture worktree is dirty: %q", got)
	}
	roadmap, err := os.ReadFile(filepath.Join(repo, "docs/planning/roadmap-04-combat-workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(roadmap), "- [ ] ") != 0 || !strings.Contains(string(roadmap), "Status: Complete") {
		t.Fatalf("fixture roadmap was not completed:\n%s", roadmap)
	}
}

func TestPriority5WorkflowRejectsProtectedBoundaryMutation(t *testing.T) {
	repo := newPriority5FixtureRepo(t)
	w := loadPriority5Fixture(t, repo)
	p := &priority5FixtureProvider{protectFiles: true}
	e := newPriority5FixtureEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "protected integrity rule repository-instructions changed") {
		t.Fatalf("protected mutation error = %v", err)
	}
	if _, ok, _ := e.Store.Resolve("complete"); ok {
		t.Fatal("protected mutation reached completion")
	}
}

func loadPriority5Fixture(t *testing.T, repo string) *workflow.Workflow {
	t.Helper()
	document, err := workflow.Decode(filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if result := workflow.Validate(document); result.Status != workflow.Executable {
		t.Fatalf("priority5 fixture status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	w := document.Workflow
	w.Spec.Parameters["repo_root"] = workflow.Parameter{Type: "path", Default: repo}
	w.Spec.Parameters["require_visual_confirm"] = workflow.Parameter{Type: "boolean", Default: false}
	for i := range w.Spec.Preconditions {
		if w.Spec.Preconditions[i].Type == "commands-exist" {
			w.Spec.Preconditions[i].Commands = []string{"git", "sh"}
		}
	}
	return w
}

func newPriority5FixtureEngine(t *testing.T, w *workflow.Workflow, p provider.Provider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"codex": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func newPriority5FixtureRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	files := map[string]string{
		"AGENTS.md": "fixture instructions\n",
		"docs/planning/roadmap-04-combat-workflow.md": "Status: In Progress\n\n" +
			"- [ ] A catalogued equipped weapon can resolve a complete attack.\n" +
			"- [ ] Advantage and disadvantage follow engine rules and are logged.\n" +
			"- [ ] Armor, wounds, bleeding, and death outcomes match deterministic tests.\n" +
			"- [ ] The mutation and activity record commit atomically.\n" +
			"- [ ] Player/Warden modification boundaries remain enforced.\n" +
			"- [ ] Direct damage remains available as a Warden adjudication tool.\n",
		"docs/planning/README.md":       "5. [ ] [Complete Combat Workflow]\n",
		"scripts/check.sh":              "#!/bin/sh\nexit 0\n",
		".github/workflows/quality.yml": "run: sh scripts/check.sh\n",
	}
	for name, contents := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "fixture seed")
	return repo
}
