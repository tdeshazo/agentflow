package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type checklistProvider struct {
	calls    int
	closeAll bool
}

func (p *checklistProvider) Name() string { return "checklist" }
func (p *checklistProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.calls++
	path := filepath.Join(request.Workspace, "progress.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return provider.Result{}, err
	}
	if p.closeAll {
		b = []byte(strings.ReplaceAll(string(b), "- [ ]", "- [x]"))
	} else {
		b = []byte(strings.Replace(string(b), "- [ ]", "- [x]", 1))
	}
	return provider.Result{}, os.WriteFile(path, b, 0o644)
}

func TestControlFlowEndToEnd(t *testing.T) {
	cases := []struct {
		name       string
		max        int
		closeAll   bool
		wantCalls  int
		errContain string
	}{
		{name: "bounded next unchecked loop and false conditions", max: 3, wantCalls: 2},
		{name: "loop bound is enforced", max: 1, wantCalls: 1, errContain: "exceeded maxIterations=1"},
		{name: "unrelated progress closure fails", max: 3, closeAll: true, wantCalls: 1, errContain: "unrelated criterion was checked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newControlFlowRepo(t)
			p := &checklistProvider{closeAll: tc.closeAll}
			e, err := New(controlFlowWorkflow(t, repo, tc.max), map[string]provider.Provider{"test": p}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			err = e.Run(context.Background())
			if tc.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error = %v, want %q", err, tc.errContain)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if p.calls != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d", p.calls, tc.wantCalls)
			}
			if tc.errContain == "" {
				if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
					t.Fatalf("completion marker: ok=%v err=%v", ok, err)
				}
				progress, err := os.ReadFile(filepath.Join(repo, "progress.md"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(progress), "- [ ]") {
					t.Fatalf("unchecked progress remains: %s", progress)
				}
				if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
					t.Fatalf("worktree dirty: %q", got)
				}
			}
		})
	}
}

func TestConditionTypeFailurePreventsActorExecution(t *testing.T) {
	repo := newControlFlowRepo(t)
	w := controlFlowWorkflow(t, repo, 3)
	w.Spec.Parameters["run_optional"] = workflow.Parameter{Type: "string", Default: "false"}
	w.Spec.Flow = []workflow.FlowStep{{Phase: "optional"}}
	p := &checklistProvider{}
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not boolean") {
		t.Fatalf("error = %v, want boolean type failure", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", p.calls)
	}
}

func TestLoopDispatchUsesStableCriterionIDs(t *testing.T) {
	repo := newControlFlowRepo(t)
	w := controlFlowWorkflow(t, repo, 3)
	for i := range w.Spec.Phases {
		phase := &w.Spec.Phases[i]
		if phase.Kind != "criterion" {
			continue
		}
		phase.CriterionID = phase.Criterion
		phase.Criterion = ""
	}
	for i := range w.Spec.Flow {
		if w.Spec.Flow[i].Loop != nil {
			w.Spec.Flow[i].Loop.DispatchByCriterion = map[string]string{"one": "one", "two": "two"}
		}
	}
	p := &checklistProvider{}
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", p.calls)
	}
}

func newControlFlowRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "progress.md"), []byte("- [ ] first\n- [ ] second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "progress.md")
	gitIn(t, repo, "commit", "-qm", "seed")
	return repo
}

func gitIn(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	b, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return string(b)
}

func controlFlowWorkflow(t *testing.T, repo string, max int) *workflow.Workflow {
	t.Helper()
	document, err := workflow.Decode(filepath.Join("testdata", "control-flow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	result := workflow.Validate(document)
	if result.Status != workflow.Executable {
		t.Fatalf("fixture status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	w := document.Workflow
	w.Spec.Parameters["repo_root"] = workflow.Parameter{Type: "path", Default: repo}
	w.Spec.Parameters["max_steps"] = workflow.Parameter{Type: "integer", Default: max}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test"}
	return w
}
