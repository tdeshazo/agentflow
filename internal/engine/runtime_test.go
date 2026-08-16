package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

type writeProvider struct {
	calls int
}

func (p *writeProvider) Name() string { return "test" }
func (p *writeProvider) Run(_ context.Context, req provider.Request) (provider.Result, error) {
	p.calls++
	return provider.Result{}, os.WriteFile(filepath.Join(req.Workspace, "work.txt"), []byte("done\n"), 0o644)
}

func TestRunPersistsCompletionInGitRefs(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
		return string(b)
	}
	git("init", "-q")
	git("config", "user.name", "AgentFlow Test")
	git("config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-qm", "seed")

	w := &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha1",
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: "runtime-test"},
		Spec: workflow.Spec{
			Parameters: map[string]workflow.Parameter{"repo_root": {Type: "path", Default: repo}},
			Workspace: workflow.WorkspaceSpec{
				Root:           "{{ parameters.repo_root }}",
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt"}},
				Checkpointing:  workflow.CheckpointSpec{CommitMessage: "test: {{ phase.label }}"},
			},
			Agents: map[string]workflow.Agent{"worker": {Runner: "test"}},
			Tools: map[string]workflow.Tool{
				"scope":      {Type: "workspace-policy"},
				"gate":       {Type: "shell", Command: "true"},
				"checkpoint": {Type: "git-checkpoint"},
			},
			Validation: map[string]workflow.Validation{
				"phaseGate": {Steps: []workflow.ToolUse{{Uses: "scope"}, {Uses: "gate"}}},
			},
			PhaseDefaults: workflow.PhaseDefaults{After: []workflow.PhaseAction{
				{Validate: "phaseGate"},
				{Checkpoint: "checkpoint"},
				{AssertNetRepositoryChangeSincePhaseStart: true},
				{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
				{ClearActivePhase: true},
			}},
			Phases:     []workflow.Phase{{ID: "01", Kind: "implementation", Label: "write", Actor: "worker", RequiresChange: true, Prompt: "write"}},
			Flow:       []workflow.FlowStep{{Phase: "01"}, {Complete: "workflow"}},
			Completion: map[string]workflow.Completion{"workflow": {}},
		},
	}

	p := &writeProvider{}
	providers := map[string]provider.Provider{"test": p}
	e, err := New(w, providers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e.Out = os.Stderr
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", p.calls)
	}
	if _, ok, err := e.Store.Resolve("complete"); err != nil || !ok {
		t.Fatalf("complete ref: ok=%v err=%v", ok, err)
	}
	if got := git("status", "--porcelain"); got != "" {
		t.Fatalf("worktree not clean: %q", got)
	}

	// A fresh engine process-equivalent instance sees the same Git-backed state.
	e2, err := New(w, providers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e2.Out = os.Stderr
	if err := e2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider reran after persisted completion; calls = %d", p.calls)
	}
}

func TestRunCheckGitLineage(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
		return string(b)
	}
	git("init", "-q")
	git("config", "user.name", "AgentFlow Test")
	git("config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-am", "feature")

	w := &workflow.Workflow{
		Metadata: workflow.Metadata{Name: "lineage-check"},
		Spec: workflow.Spec{
			Workspace:  workflow.WorkspaceSpec{Root: repo},
			Parameters: map[string]workflow.Parameter{"repo_root": {Type: "path", Default: repo}},
		},
	}
	e := &Engine{Workflow: w, Repo: gitstate.Repo{Root: repo}, Parameters: map[string]any{"repo_root": repo}}
	if err := e.runCheck(workflow.Check{
		Type:                  "git-lineage",
		Base:                  strings.TrimSpace(base),
		RequireAncestorOfHead: true,
		RequireBranch:         "feature",
	}); err != nil {
		t.Fatalf("git-lineage check failed: %v", err)
	}
}
