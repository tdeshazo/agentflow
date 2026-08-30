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

func TestV1Alpha3TypedContractsAuthorizeOnlyVerifiedHandoffs(t *testing.T) {
	repo := newDurableRepo(t)
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "implement" {
			return nil
		}
		path := filepath.Join(request.Workspace, "src", "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("accepted\n"), 0o644)
	}}
	engine := newTypedContractEngine(t, repo, providerImpl)
	if err := engine.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(providerImpl.calls, ","); got != "implement:worker,audit:auditor" {
		t.Fatalf("actor calls = %q", got)
	}
	var artifact ContractArtifact
	if ok, err := engine.Store.GetJSON(engine.contractArtifactRecord("implement", "result"), &artifact); err != nil || !ok || len(artifact.Files) != 1 {
		t.Fatalf("artifact record = %#v, ok=%t, err=%v", artifact, ok, err)
	}
	var evidence ContractEvidence
	if ok, err := engine.Store.GetJSON(engine.contractEvidenceRecord("audit", "audit-accepted"), &evidence); err != nil || !ok || evidence.Validation != "audit-gate" {
		t.Fatalf("evidence record = %#v, ok=%t, err=%v", evidence, ok, err)
	}
}

func TestV1Alpha3ReadOnlyAuditRejectsWorkspaceMutation(t *testing.T) {
	repo := newDurableRepo(t)
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		switch request.Metadata["phase"] {
		case "implement":
			path := filepath.Join(request.Workspace, "src", "result.txt")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("accepted\n"), 0o644)
		case "audit":
			return os.WriteFile(filepath.Join(request.Workspace, "src", "audit.txt"), []byte("forbidden\n"), 0o644)
		}
		return nil
	}}
	engine := newTypedContractEngine(t, repo, providerImpl)
	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "read-only audit phase audit changed workspace paths") {
		t.Fatalf("run error = %v", err)
	}
	if got := strings.Join(providerImpl.calls, ","); got != "implement:worker,audit:auditor" {
		t.Fatalf("actor calls = %q", got)
	}
}

func TestV1Alpha3ArtifactInputRejectsChangedProducerContentBeforeAudit(t *testing.T) {
	repo := newDurableRepo(t)
	providerImpl := &schedulingProvider{skipPhaseFile: true}
	engine := newTypedContractEngine(t, repo, providerImpl)
	path := filepath.Join(repo, "src", "result.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("accepted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	producer, err := engine.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	record, err := engine.captureContractArtifact(producer, "result", engine.Workflow.Spec.Contracts.Artifacts["result"])
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.SetJSON(engine.contractArtifactRecord("implement", "result"), record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	consumer, err := engine.phaseByID("audit")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.validateContractArtifactInput(consumer, "result"); err == nil || !strings.Contains(err.Error(), "no longer matches producer identity") {
		t.Fatalf("artifact input error = %v", err)
	}
}

func newTypedContractEngine(t *testing.T, repo string, providerImpl *schedulingProvider) *Engine {
	t.Helper()
	workflowDefinition := &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha3",
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: "typed-contracts"},
		Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{MutationPolicy: workflow.MutationPolicy{Allowed: []string{"src/**"}}},
			Agents: map[string]workflow.Agent{
				"worker":  {Runner: "test", Model: "worker", MayCommit: false},
				"auditor": {Runner: "test", Model: "auditor", MayCommit: false},
			},
			Tools: map[string]workflow.Tool{"gate": {Type: "shell", Command: "test -f src/result.txt"}},
			Validation: map[string]workflow.Validation{
				"implementation-gate": {Steps: []workflow.ToolUse{{Uses: "gate"}}, ProducesEvidence: []string{"implementation-accepted"}},
				"audit-gate":          {Steps: []workflow.ToolUse{{Uses: "gate"}}, ProducesEvidence: []string{"audit-accepted"}},
			},
			Contracts: workflow.ContractSpec{
				Artifacts: map[string]workflow.Artifact{"result": {Type: "files", Paths: []string{"src/result.txt"}, Persistence: "workspace"}},
				Evidence:  map[string]workflow.Evidence{"implementation-accepted": {Type: "validation"}, "audit-accepted": {Type: "validation"}},
			},
			Phases: []workflow.Phase{
				{ID: "implement", Kind: "implementation", Actor: "worker", Prompt: "implement", RequiresChange: true, Validation: "implementation-gate", Outputs: []string{"result"}},
				{ID: "audit", Kind: "audit", Actor: "auditor", Prompt: "audit", RequiresChange: false, Validation: "audit-gate", Inputs: []workflow.ContractInput{{Artifact: "result"}, {Evidence: "implementation-accepted"}}, IfEvidence: "implementation-accepted", ReadOnly: true},
			},
			Completion: map[string]workflow.Completion{"default": {FinalValidation: "audit-gate", Evidence: []string{"audit-accepted"}}},
		},
		DependencyGraph: workflow.PhaseDependencyGraph{
			Nodes: []workflow.PhaseDependencyNode{{ID: "implement", AuthoredOrder: 0}, {ID: "audit", AuthoredOrder: 1}},
			Edges: []workflow.PhaseDependencyEdge{{Phase: "audit", DependsOn: "implement", SatisfiedWhen: workflow.PhaseDependencyAccepted}},
		},
	}
	engine, err := New(workflowDefinition, map[string]provider.Provider{"test": providerImpl}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}
