package runtime_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	agentflowruntime "github.com/tdeshazo/agentflow/runtime"
	"github.com/tdeshazo/agentflow/tool"
)

type customConfig struct {
	Path string `yaml:"path"`
}

func TestExternalPackageCanInjectAndExecuteCustomTool(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "AgentFlow Test")
	runGit(t, repo, "config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-qm", "seed")
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewTyped(tool.Descriptor{Version: tool.ContractVersionV1, Type: "example.custom", Mutation: tool.MutationWorkspace}, func(_ context.Context, invocation tool.Invocation, config customConfig) error {
		return os.WriteFile(filepath.Join(invocation.Workspace, config.Path), []byte("custom\n"), 0o644)
	})); err != nil {
		t.Fatal(err)
	}
	r, err := agentflowruntime.New(agentflowruntime.Config{WorkflowPath: filepath.Join("testdata", "custom-tool.yaml"), RepoRoot: repo, ToolRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ExecuteTool(context.Background(), "custom"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(repo, "output.txt"))
	if err != nil || string(contents) != "custom\n" {
		t.Fatalf("output = %q, err=%v", contents, err)
	}
}

func TestExternalExecuteToolRejectsOutOfScopeMutationWithoutState(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "AgentFlow Test")
	runGit(t, repo, "config", "user.email", "agentflow@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-qm", "seed")
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewTyped(tool.Descriptor{Version: tool.ContractVersionV1, Type: "example.custom", Mutation: tool.MutationWorkspace}, func(_ context.Context, invocation tool.Invocation, _ customConfig) error {
		return os.WriteFile(filepath.Join(invocation.Workspace, "forbidden.txt"), []byte("forbidden\n"), 0o644)
	})); err != nil {
		t.Fatal(err)
	}
	r, err := agentflowruntime.New(agentflowruntime.Config{WorkflowPath: filepath.Join("testdata", "custom-tool.yaml"), RepoRoot: repo, ToolRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ExecuteTool(context.Background(), "custom"); err == nil || !strings.Contains(err.Error(), "out-of-scope file changed: forbidden.txt") {
		t.Fatalf("out-of-scope plugin mutation result = %v", err)
	}
	refs := gitOutput(t, repo, "for-each-ref", "--format=%(refname)", "refs/agentflow")
	if strings.Contains(refs, "validation-evidence") || strings.Contains(refs, "workflow-complete") {
		t.Fatalf("rejected tool published accepted evidence/state: %q", refs)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}
