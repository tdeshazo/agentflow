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

type handoffContractProvider struct {
	legacyStage7Provider
	contract provider.Contract
}

func (p *handoffContractProvider) Contract() provider.Contract { return p.contract }

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

func TestNewRejectsUnsupportedProvidersForStructuredHandoffPhases(t *testing.T) {
	providerV1 := provider.Contract{Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent}, InvocationContextVersions: []string{provider.InvocationContextVersionV1}, FilesystemBoundary: true, ExecutionPolicy: true}
	providerV2WithoutHandoff := provider.Contract{Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent}, InvocationContextVersions: []string{provider.InvocationContextVersionV1, provider.InvocationContextVersionV2}, FilesystemBoundary: true, ExecutionPolicy: true}
	providerV2WithoutFreshContext := provider.Contract{Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent}, InvocationContextVersions: []string{provider.InvocationContextVersionV1}, FilesystemBoundary: true, ExecutionPolicy: true, HandoffVersions: []string{provider.HandoffVersionV1}}
	for _, phaseShape := range []struct {
		name string
		set  func(*workflow.Phase)
	}{
		{name: "audit", set: func(phase *workflow.Phase) { phase.Kind = "audit"; phase.RequiresChange = false }},
		{name: "declared outputs", set: func(phase *workflow.Phase) { phase.Outputs = []string{"result"} }},
	} {
		for _, providerShape := range []struct {
			name string
			new  func() provider.Provider
		}{
			{name: "contractless", new: func() provider.Provider { return &legacyStage7Provider{} }},
			{name: "provider v1", new: func() provider.Provider { return &handoffContractProvider{contract: providerV1} }},
			{name: "provider v2 without handoff", new: func() provider.Provider { return &handoffContractProvider{contract: providerV2WithoutHandoff} }},
			{name: "provider v2 without fresh context", new: func() provider.Provider { return &handoffContractProvider{contract: providerV2WithoutFreshContext} }},
		} {
			t.Run(phaseShape.name+"/"+providerShape.name, func(t *testing.T) {
				repo := newDurableRepo(t)
				w := durableWorkflow(repo, "structured-preflight")
				phaseShape.set(&w.Spec.Phases[0])
				candidate := providerShape.new()
				_, err := New(w, map[string]provider.Provider{"test": candidate}, Options{})
				if err == nil || !strings.Contains(err.Error(), provider.ContractVersionV2) || !strings.Contains(err.Error(), provider.InvocationContextVersionV2) || !strings.Contains(err.Error(), provider.HandoffVersionV1) {
					t.Fatalf("New() error = %v, want provider/v2 plus invocation-context/v2 and handoff/v1 rejection", err)
				}
				var calls int
				switch typed := candidate.(type) {
				case *legacyStage7Provider:
					calls = typed.calls
				case *handoffContractProvider:
					calls = typed.calls
				}
				if calls != 0 {
					t.Fatalf("provider calls = %d, want pre-invocation rejection", calls)
				}
			})
		}
	}
}

func TestNewRejectsIncompatibleStructuredPhaseRepairProviderBeforeMutation(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "structured-repair-preflight")
	w.Spec.Phases[0].Kind = "audit"
	w.Spec.Phases[0].RequiresChange = false
	w.Spec.Validation["phaseGate"] = repairValidation()
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "legacy", Model: "legacy"}
	w.Spec.Temp.Directory = filepath.Join(repo, "must-not-exist-XXXX")
	primary := &handoffContractProvider{contract: provider.Contract{
		Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV1, provider.InvocationContextVersionV2},
		FilesystemBoundary:        true, ExecutionPolicy: true, HandoffVersions: []string{provider.HandoffVersionV1},
	}}
	repair := &handoffContractProvider{contract: provider.Contract{
		Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV1}, FilesystemBoundary: true, ExecutionPolicy: true,
	}}
	_, err := New(w, map[string]provider.Provider{"test": primary, "legacy": repair}, Options{})
	if err == nil || !strings.Contains(err.Error(), `actor "repair"`) || !strings.Contains(err.Error(), provider.ContractVersionV2) {
		t.Fatalf("New() error = %v, want incompatible repair provider rejection", err)
	}
	if primary.calls != 0 || repair.calls != 0 {
		t.Fatalf("provider calls = primary %d repair %d, want zero", primary.calls, repair.calls)
	}
	matches, globErr := filepath.Glob(filepath.Join(repo, "must-not-exist-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("preflight created workspace: matches=%v err=%v", matches, globErr)
	}
	if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("preflight changed repository: %q", status)
	}
}

func TestNewRejectsV2OnlyProviderForOrdinaryInvocationModesBeforeMutation(t *testing.T) {
	newV1Provider := func() *handoffContractProvider {
		return &handoffContractProvider{contract: provider.Contract{
			Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
			InvocationContextVersions: []string{provider.InvocationContextVersionV1}, FilesystemBoundary: true, ExecutionPolicy: true,
		}}
	}
	newV2OnlyProvider := func(withHandoff bool) *handoffContractProvider {
		contract := provider.Contract{
			Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
			InvocationContextVersions: []string{provider.InvocationContextVersionV2}, FilesystemBoundary: true, ExecutionPolicy: true,
		}
		if withHandoff {
			contract.HandoffVersions = []string{provider.HandoffVersionV1}
		}
		return &handoffContractProvider{contract: contract}
	}

	tests := []struct {
		name      string
		configure func(*workflow.Workflow)
		providers func() map[string]provider.Provider
	}{
		{
			name: "ordinary primary actor",
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV2OnlyProvider(false)}
			},
		},
		{
			name: "ordinary repair actor",
			configure: func(w *workflow.Workflow) {
				w.Spec.Validation["phaseGate"] = repairValidation()
				w.Spec.Agents["repair"] = workflow.Agent{Runner: "repair-v2", Model: "repair"}
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV1Provider(), "repair-v2": newV2OnlyProvider(false)}
			},
		},
		{
			name: "actor reachable in ordinary and structured modes",
			configure: func(w *workflow.Workflow) {
				w.Spec.Phases = append(w.Spec.Phases, workflow.Phase{ID: "audit", Kind: "audit", Actor: "worker", Validation: "phaseGate", ReadOnly: true})
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV2OnlyProvider(true)}
			},
		},
		{
			name: "standalone flow validation repair actor",
			configure: func(w *workflow.Workflow) {
				configureStandaloneRepair(w, "standalone")
				w.Spec.Flow = append([]workflow.FlowStep{{ID: "standalone-gate", Validate: "standalone"}}, w.Spec.Flow...)
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV1Provider(), "repair-v2": newV2OnlyProvider(false)}
			},
		},
		{
			name: "human gate validation repair actor",
			configure: func(w *workflow.Workflow) {
				configureStandaloneRepair(w, "human-validation")
				w.Spec.HumanGates = []workflow.HumanGate{{
					ID: "review", After: []workflow.HumanAfter{{Validation: "human-validation"}},
					Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "approve"},
				}}
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV1Provider(), "repair-v2": newV2OnlyProvider(false)}
			},
		},
		{
			name: "completion final validation repair actor",
			configure: func(w *workflow.Workflow) {
				configureStandaloneRepair(w, "final-validation")
				completion := w.Spec.Completion["done"]
				completion.FinalValidation = "final-validation"
				w.Spec.Completion["done"] = completion
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV1Provider(), "repair-v2": newV2OnlyProvider(false)}
			},
		},
		{
			name: "repair actor reachable from standalone and structured validation",
			configure: func(w *workflow.Workflow) {
				configureStandaloneRepair(w, "shared-validation")
				w.Spec.Phases = append(w.Spec.Phases, workflow.Phase{ID: "audit", Kind: "audit", Actor: "worker", Validation: "shared-validation", ReadOnly: true})
				completion := w.Spec.Completion["done"]
				completion.FinalValidation = "shared-validation"
				w.Spec.Completion["done"] = completion
			},
			providers: func() map[string]provider.Provider {
				return map[string]provider.Provider{"test": newV1Provider(), "repair-v2": newV2OnlyProvider(true)}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := durableWorkflow(repo, "ordinary-context-preflight")
			if test.configure != nil {
				test.configure(w)
			}
			w.Spec.Temp.Directory = filepath.Join(repo, "must-not-exist-XXXX")
			providers := test.providers()
			_, err := New(w, providers, Options{})
			if err == nil || !strings.Contains(err.Error(), provider.InvocationContextVersionV1) {
				t.Fatalf("New() error = %v, want invocation-context/v1 rejection", err)
			}
			for name, providerImpl := range providers {
				if calls := providerImpl.(*handoffContractProvider).calls; calls != 0 {
					t.Fatalf("provider %q calls = %d, want zero", name, calls)
				}
			}
			matches, globErr := filepath.Glob(filepath.Join(repo, "must-not-exist-*"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("preflight created workspace: matches=%v err=%v", matches, globErr)
			}
			if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
				t.Fatalf("preflight changed repository: %q", status)
			}
		})
	}
}

func configureStandaloneRepair(w *workflow.Workflow, validation string) {
	w.Spec.Validation[validation] = repairValidation()
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "repair-v2", Model: "repair"}
}

func TestNewPreflightsPhaseDefaultBeforeRepairActorsByPhaseMode(t *testing.T) {
	contractProvider := func(contract provider.Contract) *handoffContractProvider {
		return &handoffContractProvider{contract: contract}
	}
	v1 := provider.Contract{
		Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV1}, FilesystemBoundary: true, ExecutionPolicy: true,
	}
	v2Only := provider.Contract{
		Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV2}, FilesystemBoundary: true, ExecutionPolicy: true,
		HandoffVersions: []string{provider.HandoffVersionV1},
	}
	v2Both := v2Only
	v2Both.InvocationContextVersions = []string{provider.InvocationContextVersionV1, provider.InvocationContextVersionV2}

	tests := []struct {
		name           string
		configurePhase func(*workflow.Workflow)
		repairContract provider.Contract
		want           string
	}{
		{name: "ordinary phase requires v1", repairContract: v2Only, want: provider.InvocationContextVersionV1},
		{
			name: "structured phase requires v2 and handoff",
			configurePhase: func(w *workflow.Workflow) {
				w.Spec.Phases[0].Kind = "audit"
				w.Spec.Phases[0].RequiresChange = false
			},
			repairContract: v1,
			want:           provider.ContractVersionV2,
		},
		{
			name: "mixed ordinary and structured phases require both modes",
			configurePhase: func(w *workflow.Workflow) {
				w.Spec.Phases = append(w.Spec.Phases, workflow.Phase{ID: "audit", Kind: "audit", Actor: "worker", Validation: "phaseGate", ReadOnly: true})
			},
			repairContract: v2Only,
			want:           provider.InvocationContextVersionV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := durableWorkflow(repo, "before-repair-preflight")
			w.Spec.Validation["before-gate"] = repairValidation()
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "repair", Model: "repair"}
			w.Spec.PhaseDefaults.Before = []workflow.PhaseAction{{
				PersistActivePhase: workflow.PersistActivePhase{Fields: []string{"phase_id"}},
				Validate:           "before-gate",
			}}
			if test.configurePhase != nil {
				test.configurePhase(w)
			}
			w.Spec.Temp.Directory = filepath.Join(repo, "must-not-exist-XXXX")
			primary := contractProvider(v2Both)
			repair := contractProvider(test.repairContract)
			_, err := New(w, map[string]provider.Provider{"test": primary, "repair": repair}, Options{})
			if err == nil || !strings.Contains(err.Error(), `actor "repair"`) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want repair preflight rejection containing %q", err, test.want)
			}
			if primary.calls != 0 || repair.calls != 0 {
				t.Fatalf("provider calls = primary %d repair %d, want zero", primary.calls, repair.calls)
			}
			matches, globErr := filepath.Glob(filepath.Join(repo, "must-not-exist-*"))
			if globErr != nil || len(matches) != 0 {
				t.Fatalf("preflight created workspace: matches=%v err=%v", matches, globErr)
			}
			if status := strings.TrimSpace(gitIn(t, repo, "status", "--porcelain")); status != "" {
				t.Fatalf("preflight changed repository: %q", status)
			}
		})
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
