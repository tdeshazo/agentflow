package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type referenceWorkflowProvider struct {
	calls map[string]int
}

func (p *referenceWorkflowProvider) Name() string                     { return "reference-workflow" }
func (p *referenceWorkflowProvider) EnforcesFilesystemBoundary() bool { return true }

func (p *referenceWorkflowProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	phase := request.Metadata["phase"]
	p.calls[phase]++
	if phase != "01" && phase != "02" {
		return provider.Result{}, nil
	}
	path := filepath.Join(request.Workspace, "internal", "reference-"+phase+".txt")
	return provider.Result{}, os.WriteFile(path, []byte("implemented\n"), 0o644)
}

func TestReferenceV1Alpha1WorkflowCompletesWithRuntimeOwnedLifecycle(t *testing.T) {
	reference := referenceWorkflow(t)
	result := workflow.ValidateFile(reference)
	if result.Status != workflow.Executable || result.Normalized == nil {
		t.Fatalf("reference workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}

	repo := referenceWorkflowRepository(t)
	p := &referenceWorkflowProvider{calls: map[string]int{}}
	e, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{
			RepoRoot: repo,
			Overrides: map[string]string{
				"require_human_verification": "false",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls["01"] != 1 || p.calls["02"] != 1 || p.calls["07"] != 1 || p.calls["08"] != 1 {
		t.Fatalf("provider calls = %#v, want one call for every actor-owned phase", p.calls)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("completion marker: ok=%t err=%v", ok, err)
	}

	progress, err := os.ReadFile(filepath.Join(repo, "docs", "planning", "roadmap-current.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(progress), "- [ ]") || !strings.Contains(string(progress), "Status: Complete") {
		t.Fatalf("runtime-owned progress/bookkeeping did not complete: %s", progress)
	}
	index, err := os.ReadFile(filepath.Join(repo, "docs", "planning", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "5. [x] Stage 1B runtime parity") {
		t.Fatalf("runtime-owned index transition missing: %s", index)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("completed reference workflow left a dirty workspace: %q", got)
	}

	restarted, err := New(
		result.Normalized.Workflow,
		map[string]provider.Provider{"codex": p},
		Options{RepoRoot: repo, Overrides: map[string]string{"require_human_verification": "false"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	restarted.In = strings.NewReader("")
	restarted.Out = io.Discard
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls["01"] != 1 || p.calls["02"] != 1 || p.calls["07"] != 1 || p.calls["08"] != 1 {
		t.Fatalf("completed reference workflow replayed an actor: %#v", p.calls)
	}
}

func referenceWorkflow(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate reference workflow test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "spec", "agent-workflow-v1alpha1.yaml")
}

func referenceWorkflowRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeReferenceFile := func(path, contents string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	writeReferenceFile("AGENTS.md", "reference fixture instructions\n", 0o644)
	writeReferenceFile("scripts/check.sh", "#!/bin/sh\nset -eu\ntest -f .agentflow-reference-gate\n", 0o755)
	writeReferenceFile(".github/workflows/quality.yml", "jobs:\n  quality:\n    steps:\n      - run: scripts/check.sh\n", 0o644)
	writeReferenceFile(".agentflow-reference-gate", "ready\n", 0o644)
	writeReferenceFile("docs/planning/roadmap-current.md", "Status: In progress\n- [ ] First acceptance criterion text\n- [ ] Second acceptance criterion text\n", 0o644)
	writeReferenceFile("docs/planning/README.md", "5. [ ] Stage 1B runtime parity\n", 0o644)
	writeReferenceFile("internal/.keep", "fixture\n", 0o644)
	writeReferenceFile("bin/codex", "#!/bin/sh\nexit 0\n", 0o755)

	gitIn(t, repo, "init", "-q")
	gitIn(t, repo, "config", "user.name", "AgentFlow Test")
	gitIn(t, repo, "config", "user.email", "agentflow@example.invalid")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "reference fixture")
	t.Setenv("PATH", filepath.Join(repo, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return repo
}
