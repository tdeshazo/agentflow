package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	"github.com/tdeshazo/agentflow/provider/codex"
)

type presentationRecordingProvider struct {
	request provider.Request
}

func (p *presentationRecordingProvider) Name() string { return "presentation-test" }

func (p *presentationRecordingProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.request = request
	return provider.Result{}, nil
}

func TestRunAgentUsesRuntimeOwnedPresentationIntent(t *testing.T) {
	for _, test := range []struct {
		name     string
		color    string
		detached bool
		want     provider.PresentationIntent
	}{
		{name: "workflow always is ignored", color: "always", want: provider.PresentationAuto},
		{name: "omitted defaults to auto", want: provider.PresentationAuto},
		{name: "unknown defaults to auto", color: "unsupported", want: provider.PresentationAuto},
		{name: "detached is always plain", color: "always", detached: true, want: provider.PresentationPlain},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerImpl := &presentationRecordingProvider{}
			e := &Engine{
				Workflow: &workflow.Workflow{
					Spec: workflow.Spec{
						Agents: map[string]workflow.Agent{
							"worker": {Runner: "test", Color: test.color},
						},
					},
				},
				Providers: map[string]provider.Provider{"test": providerImpl},
				detached:  test.detached,
			}

			if err := e.runAgent(context.Background(), "worker", "high", "do work", nil); err != nil {
				t.Fatal(err)
			}
			if providerImpl.request.Presentation != test.want {
				t.Fatalf("presentation intent = %q, want %q", providerImpl.request.Presentation, test.want)
			}
		})
	}
}

type capabilityRecordingProvider struct {
	request provider.Request
	result  provider.Result
}

func (p *capabilityRecordingProvider) Name() string { return "capability-test" }

func (p *capabilityRecordingProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.request = request
	return p.result, nil
}

func TestRunAgentV1Alpha2ProviderCapabilitiesMatchSharedAgent(t *testing.T) {
	document := decodeV1Alpha2CapabilityDocument(t,
		"runner: codex, model: capability-model, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true",
		"true",
	)
	authored := document.V1Alpha2.Spec.Agents["worker"]
	if authored.Sandbox != "workspace-write" || authored.Approval != "never" || !authored.Ephemeral || !authored.MayCommit || !authored.OutputLastMessage {
		t.Fatalf("authored v1alpha2 agent lost capabilities: %#v", authored)
	}

	v1alpha2Agent := document.Workflow.Spec.Agents["worker"]
	if v1alpha2Agent.Sandbox != authored.Sandbox || v1alpha2Agent.Approval != authored.Approval || v1alpha2Agent.Ephemeral != authored.Ephemeral || v1alpha2Agent.MayCommit != authored.MayCommit || v1alpha2Agent.OutputLastMessage != authored.OutputLastMessage {
		t.Fatalf("normalized shared agent = %#v, authored v1alpha2 agent = %#v", v1alpha2Agent, authored)
	}
	sharedAgent := workflow.Agent{
		Runner: "codex", Model: "capability-model", Sandbox: "workspace-write", Approval: "never", Ephemeral: true, MayCommit: true, OutputLastMessage: true,
	}
	var providerRequests []provider.Request
	for _, test := range []struct {
		name  string
		agent workflow.Agent
	}{
		{name: "normalized v1alpha2", agent: v1alpha2Agent},
		{name: "equivalent shared agent", agent: sharedAgent},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerImpl := &capabilityRecordingProvider{}
			e := &Engine{
				Workflow:  &workflow.Workflow{Spec: workflow.Spec{Agents: map[string]workflow.Agent{"worker": test.agent}}},
				Providers: map[string]provider.Provider{"codex": providerImpl},
			}
			if err := e.runAgent(context.Background(), "worker", "", "do work", nil); err != nil {
				t.Fatal(err)
			}
			got := providerImpl.request
			if got.Sandbox != "workspace-write" || got.Approval != "never" || !got.Ephemeral {
				t.Fatalf("provider request capabilities = %#v", got)
			}
			providerRequests = append(providerRequests, got)
		})
	}
	if !reflect.DeepEqual(providerRequests[0], providerRequests[1]) {
		t.Fatalf("normalized v1alpha2 provider request = %#v, shared agent provider request = %#v", providerRequests[0], providerRequests[1])
	}
}

func TestV1Alpha2MayCommitUsesSharedLifecyclePolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		agent     func(t *testing.T) workflow.Agent
		mayCommit bool
	}{
		{
			name: "normalized v1alpha2 denies commits when false",
			agent: func(t *testing.T) workflow.Agent {
				return decodeV1Alpha2CapabilityDocument(t, "runner: codex, model: capability-model, may_commit: false", "true").Workflow.Spec.Agents["worker"]
			},
		},
		{
			name: "equivalent shared agent denies commits when false",
			agent: func(*testing.T) workflow.Agent {
				return workflow.Agent{Runner: "codex", Model: "capability-model", MayCommit: false}
			},
		},
		{
			name: "normalized v1alpha2 allows commits when true",
			agent: func(t *testing.T) workflow.Agent {
				return decodeV1Alpha2CapabilityDocument(t, "runner: codex, model: capability-model, may_commit: true", "true").Workflow.Spec.Agents["worker"]
			},
			mayCommit: true,
		},
		{
			name: "equivalent shared agent allows commits when true",
			agent: func(*testing.T) workflow.Agent {
				return workflow.Agent{Runner: "codex", Model: "capability-model", MayCommit: true}
			},
			mayCommit: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			e, err := New(&workflow.Workflow{
				Metadata: workflow.Metadata{Name: "may-commit"},
				Spec:     workflow.Spec{Agents: map[string]workflow.Agent{"worker": test.agent(t)}},
			}, nil, Options{RepoRoot: repo})
			if err != nil {
				t.Fatal(err)
			}
			start, err := e.Repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "agent-change.txt"), []byte("change\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "add", "agent-change.txt")
			gitIn(t, repo, "commit", "-qm", "agent change")

			err = e.assertAgentCommitPolicy(&workflow.Phase{ID: "work", Actor: "worker"}, ActivePhase{StartCommit: start})
			if test.mayCommit {
				if err != nil {
					t.Fatalf("commit policy error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "not allowed to commit") {
				t.Fatalf("commit policy error = %v", err)
			}
		})
	}
}

func TestV1Alpha2OutputLastMessageUsesSharedExecutionSemantics(t *testing.T) {
	// The shared provider boundary has no per-agent final-message setting: the
	// Codex adapter captures its final message for every invocation, and phase
	// acceptance deliberately ignores provider.Result. Preserve that behavior
	// for normalized v1alpha2 agents instead of inventing a second path.
	var providerRequests []provider.Request
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			v1alpha2Agent := decodeV1Alpha2CapabilityDocument(t,
				fmt.Sprintf("runner: codex, model: capability-model, output_last_message: %t", enabled),
				"true",
			).Workflow.Spec.Agents["worker"]
			if v1alpha2Agent.OutputLastMessage != enabled {
				t.Fatalf("normalized output_last_message = %t, want %t", v1alpha2Agent.OutputLastMessage, enabled)
			}
			sharedAgent := workflow.Agent{Runner: "codex", Model: "capability-model", OutputLastMessage: enabled}

			for _, test := range []struct {
				name  string
				agent workflow.Agent
			}{
				{name: "normalized v1alpha2", agent: v1alpha2Agent},
				{name: "equivalent shared agent", agent: sharedAgent},
			} {
				t.Run(test.name, func(t *testing.T) {
					providerImpl := &capabilityRecordingProvider{result: provider.Result{FinalMessage: "provider final message"}}
					e := &Engine{
						Workflow:  &workflow.Workflow{Spec: workflow.Spec{Agents: map[string]workflow.Agent{"worker": test.agent}}},
						Providers: map[string]provider.Provider{"codex": providerImpl},
					}
					if err := e.runAgent(context.Background(), "worker", "", "do work", nil); err != nil {
						t.Fatal(err)
					}
					if providerImpl.request.Metadata["actor"] != "worker" {
						t.Fatalf("provider request = %#v", providerImpl.request)
					}
					providerRequests = append(providerRequests, providerImpl.request)
				})
			}
		})
	}
	for i := 1; i < len(providerRequests); i++ {
		if !reflect.DeepEqual(providerRequests[i], providerRequests[0]) {
			t.Fatalf("output_last_message changed the shared provider request:\n got: %#v\nwant: %#v", providerRequests[i], providerRequests[0])
		}
	}
}

func TestV1Alpha2UnsupportedApprovalFailsAtProviderExecutionBoundary(t *testing.T) {
	repo := newDurableRepo(t)
	validationMarker := filepath.Join(repo, "validation-ran")
	document := decodeV1Alpha2CapabilityDocument(t,
		"runner: codex, model: capability-model, approval: on-request",
		fmt.Sprintf("touch %q", validationMarker),
	)
	e, err := New(document.Workflow, map[string]provider.Provider{"codex": codex.Provider{}}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard

	err = e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "codex provider supports approval policy") {
		t.Fatalf("run error = %v", err)
	}
	if _, err := os.Stat(validationMarker); !os.IsNotExist(err) {
		t.Fatalf("validation ran after actor capability failure: stat error = %v", err)
	}
	phase, err := e.phaseByID("work")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("actor capability failure accepted phase: ok=%v err=%v", ok, err)
	}
}

func decodeV1Alpha2CapabilityDocument(t *testing.T, agent, validation string) *workflow.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	document := fmt.Sprintf(`
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: capability-test}
spec:
  workspace: {allowWrites: ["*"]}
  agents:
    worker: {%s}
  validation:
    gate: {run: %q}
  phases:
    - {id: work, actor: worker, prompt: do work, validation: gate}
  completion: {validation: gate}
`, agent, validation)
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	decoded, err := workflow.Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
