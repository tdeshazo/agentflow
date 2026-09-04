package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	if len(providerImpl.contexts) != 2 {
		t.Fatalf("compiled contexts = %d", len(providerImpl.contexts))
	}
	auditContext := providerImpl.contexts[1]
	if auditContext.Version != provider.InvocationContextVersionV2 || auditContext.Invocation.Phase != "audit" || !auditContext.Authority.ReadOnly || len(auditContext.Authority.WritablePaths) != 0 {
		t.Fatalf("audit context identity/authority = %#v", auditContext)
	}
	if len(auditContext.Dependencies) != 1 || auditContext.Dependencies[0].Phase != "implement" || auditContext.Dependencies[0].Commit == "" {
		t.Fatalf("audit dependencies = %#v", auditContext.Dependencies)
	}
	if len(auditContext.Artifacts) != 1 || auditContext.Artifacts[0].Name != "result" || auditContext.Artifacts[0].Path != provider.WorkspacePlaceholder+"/src/result.txt" || auditContext.Artifacts[0].Digest == "" {
		t.Fatalf("audit artifacts = %#v", auditContext.Artifacts)
	}
	if len(auditContext.Evidence) != 1 || auditContext.Evidence[0].Name != "implementation-accepted" {
		t.Fatalf("audit evidence = %#v", auditContext.Evidence)
	}
	if strings.Contains(fmt.Sprintf("%#v", auditContext), "audit-accepted") {
		t.Fatalf("audit context included undeclared downstream evidence: %#v", auditContext.Evidence)
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

func TestInvocationContextCompilationIsDeterministic(t *testing.T) {
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
	e := newTypedContractEngine(t, repo, providerImpl)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("audit")
	if err != nil {
		t.Fatal(err)
	}
	agent := e.Workflow.Spec.Agents[phase.Actor]
	first, err := e.compileInvocationContext(phase.Actor, invocationRolePhase, phase.Prompt, agent, phase, []string{phase.Validation})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.compileInvocationContext(phase.Actor, invocationRolePhase, phase.Prompt, agent, phase, []string{phase.Validation})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent authoritative state produced different contexts:\nfirst=%#v\nsecond=%#v", first, second)
	}
	changedObjective, err := e.compileInvocationContext(phase.Actor, invocationRolePhase, "different objective", agent, phase, []string{phase.Validation})
	if err != nil {
		t.Fatal(err)
	}
	want := first
	want.Objective = "different objective"
	if !reflect.DeepEqual(want, changedObjective) {
		t.Fatalf("objective change affected unrelated context components")
	}
}

func TestV1Alpha3ArtifactCaptureFailureDoesNotAcceptProducer(t *testing.T) {
	repo := newDurableRepo(t)
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "implement" {
			return nil
		}
		path := filepath.Join(request.Workspace, "src", "other.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("unrelated\n"), 0o644)
	}}
	engine := newTypedContractEngine(t, repo, providerImpl)
	engine.Workflow.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "true"}

	err := engine.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `artifact "result" producer phase implement did not produce declared path "src/result.txt"`) {
		t.Fatalf("run error = %v", err)
	}
	phase, err := engine.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := engine.validCommitMarker(engine.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("artifact capture failure accepted producer: ok=%t err=%v", ok, err)
	}
	var active ActivePhase
	if ok, err := engine.Store.GetJSON(engine.activeRecord(), &active); err != nil || !ok || !active.ActorCompleted {
		t.Fatalf("active phase = %#v, ok=%t, err=%v", active, ok, err)
	}
	var artifact ContractArtifact
	if ok, err := engine.Store.GetJSON(engine.contractArtifactRecord("implement", "result"), &artifact); err != nil || ok {
		t.Fatalf("artifact record = %#v, ok=%t, err=%v", artifact, ok, err)
	}

	restarted := newTypedContractEngine(t, repo, providerImpl)
	restarted.Workflow.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "true"}
	err = restarted.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `artifact "result" producer phase implement did not produce declared path "src/result.txt"`) {
		t.Fatalf("restart error = %v", err)
	}
	if got := strings.Join(providerImpl.calls, ","); got != "implement:worker" {
		t.Fatalf("actor calls after restart = %q", got)
	}
	if ok, _, err := restarted.validCommitMarker(restarted.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("restart accepted producer without artifact: ok=%t err=%v", ok, err)
	}
}

func TestV1Alpha3RestartFinishesArtifactPersistenceInterruptedBeforeMarker(t *testing.T) {
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
	interrupted := newTypedContractEngine(t, repo, providerImpl)
	if err := interrupted.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := interrupted.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	active, err := interrupted.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Store.SetJSON(interrupted.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.runPhaseActor(context.Background(), phase, phase.Prompt, &active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.checkpoint(phase.Label, phase); err != nil {
		t.Fatal(err)
	}
	active.CheckpointCommit, err = interrupted.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Store.SetJSON(interrupted.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.persistPhaseContractOutputs(phase); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := interrupted.validCommitMarker(interrupted.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("interrupted producer marker: ok=%t err=%v", ok, err)
	}

	restarted := newTypedContractEngine(t, repo, providerImpl)
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(providerImpl.calls, ","); got != "implement:worker,audit:auditor" {
		t.Fatalf("actor calls after restart = %q", got)
	}
	if ok, _, err := restarted.validCommitMarker(restarted.phaseMarkerName(phase)); err != nil || !ok {
		t.Fatalf("restarted producer marker: ok=%t err=%v", ok, err)
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

func TestV1Alpha3IncompatibleDurableArtifactFailsBeforeProviderExecution(t *testing.T) {
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
	e := newTypedContractEngine(t, repo, providerImpl)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := e.runPhase(context.Background(), "implement"); err != nil {
		t.Fatal(err)
	}
	var record ContractArtifact
	if ok, err := e.Store.GetJSON(e.contractArtifactRecord("implement", "result"), &record); err != nil || !ok {
		t.Fatalf("artifact record: ok=%t err=%v", ok, err)
	}
	record.Type = "incompatible"
	if err := e.Store.SetJSON(e.contractArtifactRecord("implement", "result"), record); err != nil {
		t.Fatal(err)
	}

	err := e.runPhase(context.Background(), "audit")
	if err == nil || !strings.Contains(err.Error(), `requires compatible artifact "result"`) {
		t.Fatalf("audit error = %v", err)
	}
	if got := strings.Join(providerImpl.calls, ","); got != "implement:worker" {
		t.Fatalf("provider calls = %q, want compiler failure before audit actor", got)
	}
}

func newTypedContractEngine(t *testing.T, repo string, providerImpl *schedulingProvider) *Engine {
	t.Helper()
	providerImpl.structured = true
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
