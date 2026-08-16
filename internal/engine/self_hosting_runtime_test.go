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

// selfHostingFakeProvider is the only provider used by these tests. It edits
// temporary repositories and never invokes Codex or another model runtime.
type selfHostingFakeProvider struct {
	calls          []string
	mutation       string
	interruptPhase string
	interrupted    bool
	commitLuna     bool
}

func (p *selfHostingFakeProvider) Name() string { return "deterministic-self-hosting-fake" }

func (p *selfHostingFakeProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	actor := request.Metadata["actor"]
	phase := request.Metadata["phase"]
	p.calls = append(p.calls, actor+":"+phase)
	if p.mutation != "" {
		path := filepath.Join(request.Workspace, p.mutation)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return provider.Result{}, err
		}
		if err := os.WriteFile(path, []byte("tampered\n"), 0o644); err != nil {
			return provider.Result{}, err
		}
	} else if actor == "luna" {
		path := filepath.Join(request.Workspace, "internal/implementation.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return provider.Result{}, err
		}
		if err := os.WriteFile(path, []byte("implemented\n"), 0o644); err != nil {
			return provider.Result{}, err
		}
		if p.commitLuna && len(p.calls) == 1 {
			if output, err := runGit(request.Workspace, "add", "internal/implementation.txt"); err != nil {
				return provider.Result{}, fmt.Errorf("stage fake implementation: %w: %s", err, output)
			}
			if output, err := runGit(request.Workspace, "commit", "-qm", "fake agent implementation"); err != nil {
				return provider.Result{}, fmt.Errorf("commit fake implementation: %w: %s", err, output)
			}
		}
	} else if actor == "terra" {
		if err := os.MkdirAll(filepath.Join(request.Workspace, "docs"), 0o755); err != nil {
			return provider.Result{}, err
		}
		if err := os.WriteFile(filepath.Join(request.Workspace, "docs/audit.txt"), []byte("audited\n"), 0o644); err != nil {
			return provider.Result{}, err
		}
	}
	if phase == p.interruptPhase && !p.interrupted {
		p.interrupted = true
		return provider.Result{}, context.Canceled
	}
	return provider.Result{}, nil
}

func TestSelfHostingWorkflowRunsWithDeterministicProviders(t *testing.T) {
	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	p := &selfHostingFakeProvider{commitLuna: true}
	e := newSelfHostingEngine(t, w, p)
	e.In = strings.NewReader("yes\n")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(p.calls, ","), "luna:implement,terra:audit"; got != want {
		t.Fatalf("provider executions = %q, want distinct implementation and audit executions %q", got, want)
	}
	if _, ok, err := e.Store.Resolve("implement.done"); err != nil || !ok {
		t.Fatalf("implementation marker: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve("audit.done"); err != nil || !ok {
		t.Fatalf("audit marker: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(gitIn(t, repo, "log", "--format=%s"), "fake agent implementation") {
		t.Fatal("valid agent-created implementation commit was not preserved")
	}
	if !strings.Contains(gitIn(t, repo, "log", "--format=%s"), "independent-agentflow-audit") {
		t.Fatal("valid dirty audit work was not checkpointed")
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("completed self-hosting fixture is dirty: %q", got)
	}
	if _, ok, err := e.Store.Resolve("self-host-review"); err != nil || !ok {
		t.Fatalf("human-gate evidence: ok=%v err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker: ok=%v err=%v", ok, err)
	}

	if err := newSelfHostingEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(p.calls, ","), "luna:implement,terra:audit"; got != want {
		t.Fatalf("idempotent completion invoked providers: got %q want %q", got, want)
	}
}

func TestSelfHostingWorkflowRequiresABoundedTaskBeforeProviderExecution(t *testing.T) {
	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	p := &selfHostingFakeProvider{}
	e, err := New(w, map[string]provider.Provider{"fake": p}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "task parameter is required") {
		t.Fatalf("empty task run error = %v", err)
	}
	if len(p.calls) != 0 {
		t.Fatalf("empty task invoked providers: %v", p.calls)
	}
}

func TestSelfHostingResumeInterruptedAuditRerunsActor(t *testing.T) {
	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	p := &selfHostingFakeProvider{interruptPhase: "audit", commitLuna: true}
	e := newSelfHostingEngine(t, w, p)
	if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want cancellation", err)
	}
	if got, want := strings.Join(p.calls, ","), "luna:implement,terra:audit"; got != want {
		t.Fatalf("calls before restart = %q, want %q", got, want)
	}
	if _, ok, err := e.Store.Resolve("implement.done"); err != nil || !ok {
		t.Fatalf("implementation checkpoint marker: ok=%v err=%v", ok, err)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); !strings.Contains(got, "docs/audit.txt") {
		t.Fatalf("interrupted audit work was not preserved: %q", got)
	}

	e2 := newSelfHostingEngine(t, w, p)
	e2.In = strings.NewReader("yes\n")
	if err := e2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(p.calls, ","), "luna:implement,terra:audit,terra:audit"; got != want {
		t.Fatalf("restart did not resume interrupted audit actor: got %q want %q", got, want)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("resumed fixture is dirty: %q", got)
	}
}

func TestSelfHostingValidationRepairBudgetAndMutationPolicy(t *testing.T) {
	t.Run("failing validation cannot advance and repair is bounded", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		w := selfHostingWorkflow(t, repo)
		makeSelfHostingImplementationGateFail(w)
		p := &selfHostingFakeProvider{}
		e := newSelfHostingEngine(t, w, p)
		if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "still fails after repair") {
			t.Fatalf("failing validation error = %v", err)
		}
		if got, want := strings.Join(p.calls, ","), "luna:implement,terra:implement"; got != want {
			t.Fatalf("repair actor calls = %q, want %q", got, want)
		}
		if _, ok, _ := e.Store.Resolve("implement.done"); ok {
			t.Fatal("failing validation advanced the implementation phase")
		}
		var active ActivePhase
		if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.RepairAttempts["implementation-gate"] != 1 {
			t.Fatalf("repair budget state: active=%+v ok=%v err=%v", active, ok, err)
		}
		if err := newSelfHostingEngine(t, w, p).Run(context.Background()); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("restart error = %v, want exhausted repair budget", err)
		}
		if got, want := strings.Join(p.calls, ","), "luna:implement,terra:implement"; got != want {
			t.Fatalf("restart exceeded repair budget: got %q want %q", got, want)
		}
	})

	for _, mutation := range []struct {
		name string
		path string
		want string
	}{
		{name: "roadmap", path: "ROADMAP.md", want: "protected integrity rule roadmap changed"},
		{name: "canonical gate", path: "scripts/check.sh", want: "protected integrity rule canonical-quality-gate changed"},
		{name: "new protected research file", path: "docs/research/new-analysis.md", want: "protected integrity rule research-documents changed"},
		{name: "out of scope", path: "not-allowed.txt", want: "out-of-scope file changed: not-allowed.txt"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			repo := newSelfHostingRepo(t)
			w := selfHostingWorkflow(t, repo)
			p := &selfHostingFakeProvider{mutation: mutation.path}
			e := newSelfHostingEngine(t, w, p)
			start, err := e.Repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			err = e.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), mutation.want) {
				t.Fatalf("mutation error = %v, want %q", err, mutation.want)
			}
			if len(p.calls) != 1 {
				t.Fatalf("policy violation invoked repair/another phase: calls=%v", p.calls)
			}
			if _, ok, _ := e.Store.Resolve("complete"); ok {
				t.Fatal("policy violation reached completion")
			}
			if head, err := e.Repo.Head(); err != nil || head != start {
				t.Fatalf("policy violation was checkpointed: head=%s err=%v want=%s", head, err, start)
			}
		})
	}
}

func TestSelfHostingStatusFixtures(t *testing.T) {
	t.Run("uninitialized", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "state: uninitialized") {
			t.Fatalf("status = %s", out.String())
		}
	})

	t.Run("active", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		p := &selfHostingFakeProvider{interruptPhase: "implement"}
		e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), p)
		if err := e.Run(context.Background()); !errors.Is(err, context.Canceled) {
			t.Fatalf("run error = %v", err)
		}
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "state: active") || !strings.Contains(out.String(), "active_phase: implement") {
			t.Fatalf("status = %s", out.String())
		}
	})

	t.Run("validation failed recoverable", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		w := selfHostingWorkflow(t, repo)
		makeSelfHostingImplementationGateFail(w)
		e := newSelfHostingEngine(t, w, &selfHostingFakeProvider{})
		_ = e.Run(context.Background())
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "state: validation-failed/recoverable") || !strings.Contains(out.String(), "validation_failed: implementation-gate") {
			t.Fatalf("status = %s", out.String())
		}
	})

	t.Run("human gated", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})
		e.In = strings.NewReader("")
		if err := e.Run(context.Background()); err == nil {
			t.Fatal("human gate unexpectedly accepted empty input")
		}
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "state: human-gated") || !strings.Contains(out.String(), "human_gate: self-host-review") {
			t.Fatalf("status = %s", out.String())
		}
	})

	t.Run("completed", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		p := &selfHostingFakeProvider{commitLuna: true}
		w := selfHostingWorkflow(t, repo)
		e := newSelfHostingEngine(t, w, p)
		e.In = strings.NewReader("yes\n")
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		calls := len(p.calls)
		var out bytes.Buffer
		e.Out = &out
		if err := e.Status(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "state: completed") {
			t.Fatalf("status = %s", out.String())
		}
		if err := newSelfHostingEngine(t, w, p).Run(context.Background()); err != nil || len(p.calls) != calls {
			t.Fatalf("completion was not idempotent: err=%v calls=%v", err, p.calls)
		}
	})
}

func selfHostingWorkflow(t *testing.T, repo string) *workflow.Workflow {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "develop-agentflow.agent-workflow.yaml")
	document, err := workflow.Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	if result := workflow.Validate(document); result.Status != workflow.Executable {
		t.Fatalf("shipped self-hosting workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	w := document.Workflow
	w.Spec.Parameters["repo_root"] = workflow.Parameter{Type: "path", Default: repo}
	for i := range w.Spec.Preconditions {
		if w.Spec.Preconditions[i].Type == "commands-exist" {
			w.Spec.Preconditions[i].Commands = []string{"git", "sh", "bash"}
		}
	}
	for actor, definition := range w.Spec.Agents {
		definition.Runner = "fake"
		w.Spec.Agents[actor] = definition
	}
	return w
}

func makeSelfHostingImplementationGateFail(w *workflow.Workflow) {
	w.Spec.Tools["failing-gate"] = workflow.Tool{Type: "shell", Command: "false"}
	v := w.Spec.Validation["implementation-gate"]
	v.Steps = []workflow.ToolUse{{Uses: "assert-change-scope"}, {Uses: "failing-gate"}}
	v.OnFailure.Then = []workflow.ToolUse{{Uses: "assert-change-scope"}, {Uses: "failing-gate"}}
	w.Spec.Validation["implementation-gate"] = v
}

func newSelfHostingEngine(t *testing.T, w *workflow.Workflow, p provider.Provider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"fake": p}, Options{Overrides: map[string]string{"task": "exercise the deterministic self-hosting runtime"}})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("yes\n")
	e.Out = io.Discard
	return e
}

func newSelfHostingRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	files := map[string]string{
		"README.md":  "seed\n",
		"ROADMAP.md": "# roadmap\n",
		"docs/research/agent-workflow-orchestration-landscape.md": "research\n",
		// Bash-only syntax makes the executable-bit/shebang contract
		// observable: invoking this through `sh scripts/check.sh` must fail.
		"scripts/check.sh":                               "#!/usr/bin/env bash\nset -euo pipefail\n[[ -f README.md ]]\n",
		".github/workflows/quality.yml":                  "run: ./scripts/check.sh\n",
		"examples/develop-agentflow.agent-workflow.yaml": "self-hosting fixture definition\n",
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
	if err := os.Chmod(filepath.Join(repo, "scripts/check.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "fixture seed")
	return repo
}

func runGit(repo string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	b, err := command.CombinedOutput()
	return string(b), err
}
