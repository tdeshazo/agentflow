package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	"github.com/tdeshazo/agentflow/tool"
)

type stage7ToolConfig struct {
	Path string `yaml:"path"`
}

type lyingStage7Provider struct{ calls int }

type legacyStage7Provider struct{ calls int }

func (p *legacyStage7Provider) Name() string                     { return "legacy" }
func (p *legacyStage7Provider) EnforcesFilesystemBoundary() bool { return true }
func (p *legacyStage7Provider) EnforcesExecutionPolicy() bool    { return true }
func (p *legacyStage7Provider) Run(context.Context, provider.Request) (provider.Result, error) {
	p.calls++
	return provider.Result{}, nil
}

func (p *lyingStage7Provider) Name() string { return "liar" }
func (p *lyingStage7Provider) Contract() provider.Contract {
	return provider.Contract{Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent}, InvocationContextVersions: []string{provider.InvocationContextVersion}, FilesystemBoundary: true, ExecutionPolicy: true}
}
func (p *lyingStage7Provider) EnforcesFilesystemBoundary() bool { return false }
func (p *lyingStage7Provider) EnforcesExecutionPolicy() bool    { return true }
func (p *lyingStage7Provider) Run(context.Context, provider.Request) (provider.Result, error) {
	p.calls++
	return provider.Result{}, nil
}

func TestNewRejectsUnsatisfiedExecutorRequirementsBeforeWorkspaceMutation(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-provider-requirements")
	w.Spec.Agents["worker"] = workflow.Agent{
		Runner: "test",
		Requirements: provider.Requirements{
			ContractVersion: provider.ContractVersionV1,
			Modes:           []provider.ExecutionMode{provider.ExecutionModeNestedWorkflow},
		},
	}
	_, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "does not support execution mode") {
		t.Fatalf("New() error = %v, want rejected executor requirements", err)
	}
	if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("rejected construction changed workspace: %q", status)
	}
}

func TestProviderContractIsOptionalOnlyForZeroRequirements(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-legacy-provider")
	legacy := &legacyStage7Provider{}
	if _, err := New(w, map[string]provider.Provider{"test": legacy}, Options{}); err != nil {
		t.Fatalf("zero-requirement legacy provider rejected: %v", err)
	}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Requirements: provider.Requirements{ContractVersion: provider.ContractVersionV1}}
	if _, err := New(w, map[string]provider.Provider{"test": legacy}, Options{}); err == nil || !strings.Contains(err.Error(), "does not implement the versioned contract") {
		t.Fatalf("explicit-requirement legacy provider error = %v", err)
	}
}

func TestNewRejectsInconsistentProviderBeforeAnyMutationOrInvocation(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-lying-provider")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Requirements: provider.Requirements{ContractVersion: provider.ContractVersionV1}}
	w.Spec.Temp.Directory = filepath.Join(repo, "must-not-exist-XXXX")
	p := &lyingStage7Provider{}
	_, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err == nil || !strings.Contains(err.Error(), "filesystem enforcement claim") {
		t.Fatalf("New() error = %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want zero", p.calls)
	}
	matches, globErr := filepath.Glob(filepath.Join(repo, "must-not-exist-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("preflight created workspace: matches=%v err=%v", matches, globErr)
	}
	if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("preflight changed repository: %q", status)
	}
}

func TestNewRejectsZeroRequirementInconsistentImplementedContractBeforeMutation(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-zero-requirement-liar")
	w.Spec.Temp.Directory = filepath.Join(repo, "must-not-exist-XXXX")
	p := &lyingStage7Provider{}
	_, err := New(w, map[string]provider.Provider{"test": p}, Options{})
	if err == nil || !strings.Contains(err.Error(), "filesystem enforcement claim") {
		t.Fatalf("New() error = %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want zero", p.calls)
	}
	matches, globErr := filepath.Glob(filepath.Join(repo, "must-not-exist-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("preflight created workspace: matches=%v err=%v", matches, globErr)
	}
	if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("preflight changed repository: %q", status)
	}
}

func TestMutationNoneRejectsAnyRepositoryDeltaAndPublishesNoEvidence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-mutation-none")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"README.md", "work.txt"}
	w.Spec.Tools["reader"] = workflow.Tool{Type: "test.reader"}
	w.Spec.Validation["read"] = workflow.Validation{Dependencies: []string{"work.txt"}, Steps: []workflow.ToolUse{{Uses: "reader"}}}
	calls := 0
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewTyped(tool.Descriptor{Version: tool.ContractVersionV1, Type: "test.reader", Mutation: tool.MutationNone, BehaviorFingerprint: "v1"}, func(_ context.Context, invocation tool.Invocation, _ struct{}) error {
		calls++
		if calls == 1 {
			return os.WriteFile(filepath.Join(invocation.Workspace, "README.md"), []byte("mutated\n"), 0o644)
		}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{ToolRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "read", nil); err == nil || !strings.Contains(err.Error(), "violated MutationNone") {
		t.Fatalf("validation error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("plugin calls = %d, want 2 (no reusable evidence after mutation)", calls)
	}
}

func TestPluginFingerprintParticipatesInValidationEvidence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-plugin-fingerprint")
	w.Spec.Tools["reader"] = workflow.Tool{Type: "test.reader"}
	w.Spec.Validation["read"] = workflow.Validation{Steps: []workflow.ToolUse{{Uses: "reader"}}}
	calls := 0
	newEngine := func(fingerprint string) *Engine {
		registry := tool.NewRegistry()
		if err := registry.Register(tool.NewTyped(tool.Descriptor{Version: tool.ContractVersionV1, Type: "test.reader", Mutation: tool.MutationNone, BehaviorFingerprint: fingerprint}, func(context.Context, tool.Invocation, struct{}) error { calls++; return nil })); err != nil {
			t.Fatal(err)
		}
		e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{ToolRegistry: registry})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.initializeOrResumeState(); err != nil {
			t.Fatal(err)
		}
		return e
	}
	if err := newEngine("v1").runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if err := newEngine("v1").runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("same fingerprint calls = %d, want 1", calls)
	}
	if err := newEngine("v2").runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("changed fingerprint calls = %d, want 2", calls)
	}
	if err := newEngine("").runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if err := newEngine("").runValidation(context.Background(), "read", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("missing fingerprint calls = %d, want 4", calls)
	}
}

func TestRegisteredTypedToolExecutesWithoutCoreDispatchChanges(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stage7-custom-tool")
	w.Spec.Tools["stamp"] = workflow.Tool{
		Type:             "test.stamp",
		MutatesWorkspace: true,
		Config:           map[string]any{"path": "stamp.txt"},
	}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewTyped(
		tool.Descriptor{Version: tool.ContractVersionV1, Type: "test.stamp", Mutation: tool.MutationWorkspace},
		func(_ context.Context, invocation tool.Invocation, config stage7ToolConfig) error {
			return os.WriteFile(filepath.Join(invocation.Workspace, config.Path), []byte("stamped\n"), 0o644)
		},
	)); err != nil {
		t.Fatal(err)
	}
	e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{ToolRegistry: registry})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.runTool(context.Background(), "stamp", nil); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(repo, "stamp.txt")); err != nil || string(content) != "stamped\n" {
		t.Fatalf("custom tool output = %q, err = %v", content, err)
	}
}

func TestNewRejectsMalformedPluginConfigAndUndeclaredMutation(t *testing.T) {
	repo := newDurableRepo(t)
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewTyped(
		tool.Descriptor{Version: tool.ContractVersionV1, Type: "test.read", Mutation: tool.MutationNone},
		func(context.Context, tool.Invocation, stage7ToolConfig) error { return nil },
	)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		tool workflow.Tool
		want string
	}{
		{name: "unknown typed field", tool: workflow.Tool{Type: "test.read", Config: map[string]any{"unknown": true}}, want: "typed config"},
		{name: "undeclared mutation", tool: workflow.Tool{Type: "test.read", MutatesWorkspace: true}, want: "mutation declaration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := durableWorkflow(repo, "stage7-plugin-"+test.name)
			w.Spec.Tools["plugin"] = test.tool
			_, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{ToolRegistry: registry})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
			if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
				t.Fatalf("rejected plugin changed workspace: %q", status)
			}
		})
	}
}
