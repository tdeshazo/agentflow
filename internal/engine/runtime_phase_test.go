package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
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
				Repo:      gitstate.Repo{Root: newDurableRepo(t)},
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

func TestRunAgentEnforcesMayCommitAtEachActorInvocation(t *testing.T) {
	providerFailure := errors.New("provider failed after committing")
	for _, test := range []struct {
		name            string
		actorName       string
		actorMayCommit  bool
		commitCount     int
		providerFailure error
		wantSafety      bool
	}{
		{name: "uncommitted workspace edit is allowed", actorName: "worker"},
		{name: "disallowed actor commit", actorName: "worker", commitCount: 1, wantSafety: true},
		{name: "disallowed repair actor commit", actorName: "repair", commitCount: 1, wantSafety: true},
		{name: "disallowed actor commit after provider error", actorName: "worker", commitCount: 1, providerFailure: providerFailure, wantSafety: true},
		{name: "allowed actor may create multiple commits", actorName: "worker", actorMayCommit: true, commitCount: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			providerImpl := &capabilityActionProvider{action: func(_ context.Context, request provider.Request) error {
				if test.commitCount == 0 {
					if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("uncommitted\n"), 0o644); err != nil {
						return err
					}
				}
				for i := 0; i < test.commitCount; i++ {
					path := fmt.Sprintf("commit-%d.txt", i)
					if err := os.WriteFile(filepath.Join(request.Workspace, path), []byte("committed\n"), 0o644); err != nil {
						return err
					}
					gitIn(t, request.Workspace, "add", path)
					gitIn(t, request.Workspace, "commit", "-qm", fmt.Sprintf("actor commit %d", i))
				}
				return test.providerFailure
			}}
			e := &Engine{
				Workflow: &workflow.Workflow{Spec: workflow.Spec{Agents: map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: test.actorMayCommit},
					"repair": {Runner: "test"},
				}}},
				Repo:      gitstate.Repo{Root: repo},
				Providers: map[string]provider.Provider{"test": providerImpl},
			}

			err := e.runAgent(context.Background(), test.actorName, "", "do work", nil)
			var safetyErr *safetyViolation
			if errors.As(err, &safetyErr) != test.wantSafety {
				t.Fatalf("runAgent error = %v, safety violation = %t, want %t", err, errors.As(err, &safetyErr), test.wantSafety)
			}
			if test.wantSafety {
				if !strings.Contains(err.Error(), fmt.Sprintf("actor %q", test.actorName)) {
					t.Fatalf("policy error does not identify invoked actor: %v", err)
				}
				return
			}
			if test.providerFailure != nil && !errors.Is(err, test.providerFailure) {
				t.Fatalf("provider error = %v, want %v", err, test.providerFailure)
			}
			if test.providerFailure == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRunAgentEnforcesMayCommitDuringRecoveredActorRerun(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "recovered-may-commit")
	worker := w.Spec.Agents["worker"]
	worker.MayCommit = false
	w.Spec.Agents["worker"] = worker
	providerImpl := &durableProvider{}
	providerImpl.action = func(_ context.Context, request provider.Request) error {
		if providerImpl.calls == 1 {
			return errors.New("interrupted before work")
		}
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "recovered actor commit")
		return nil
	}

	e := newDurableEngine(t, w, providerImpl)
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "interrupted before work") {
		t.Fatalf("initial interrupted run error = %v", err)
	}
	err := newDurableEngine(t, w, providerImpl).Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "may_commit is false") {
		t.Fatalf("recovered actor commit error = %v", err)
	}
	assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
	if providerImpl.calls != 2 {
		t.Fatalf("provider calls = %d, want initial invocation plus recovered rerun", providerImpl.calls)
	}
}

func TestRunAgentEnforcesMayCommitForValidationRepairActor(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "repair-may-commit")
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: false}
	validation := w.Spec.Validation["phaseGate"]
	validation.OnFailure = workflow.FailurePolicy{
		Strategy:          "repair-once",
		MaxRepairAttempts: 1,
		Repair:            workflow.Repair{Actor: "repair", Prompt: "repair the work"},
	}
	w.Spec.Validation["phaseGate"] = validation
	providerImpl := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] != "repair" {
			return nil
		}
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "repair actor commit")
		return nil
	}}

	e := newDurableEngine(t, w, providerImpl)
	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), `actor "repair"`) {
		t.Fatalf("repair actor commit error = %v", err)
	}
	assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
	if providerImpl.calls != 2 {
		t.Fatalf("provider calls = %d, want phase actor plus repair actor", providerImpl.calls)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.FailureKind != PhaseFailureSafety {
		t.Fatalf("repair safety state = %+v ok=%v err=%v", active, ok, err)
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

type capabilityActionProvider struct {
	calls  int
	result provider.Result
	action func(context.Context, provider.Request) error
}

func (p *capabilityActionProvider) Name() string { return "capability-action-test" }

func (p *capabilityActionProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	p.calls++
	if p.action != nil {
		if err := p.action(ctx, request); err != nil {
			return provider.Result{}, err
		}
	}
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
	repo := newDurableRepo(t)
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
				Repo:      gitstate.Repo{Root: repo},
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
	// The shared provider boundary carries the agent's capture intent without
	// giving final-message output any workflow authority.
	repo := newDurableRepo(t)
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
						Repo:      gitstate.Repo{Root: repo},
					}
					if err := e.runAgent(context.Background(), "worker", "", "do work", nil); err != nil {
						t.Fatal(err)
					}
					if providerImpl.request.Metadata["actor"] != "worker" {
						t.Fatalf("provider request = %#v", providerImpl.request)
					}
					if providerImpl.request.OutputLastMessage != enabled {
						t.Fatalf("provider request output-last-message = %t, want %t", providerImpl.request.OutputLastMessage, enabled)
					}
				})
			}
		})
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

func TestV1Alpha2CapabilitiesPreserveDurableRuntimeAuthority(t *testing.T) {
	t.Run("identity rejects changed commit authority", func(t *testing.T) {
		repo := newDurableRepo(t)
		document := decodeV1Alpha2CapabilityDocument(t, "runner: test, model: capability-model, may_commit: false", "true")
		first := newCapabilityEngine(t, document.Workflow, repo, &capabilityActionProvider{})
		if err := first.Run(context.Background()); err != nil {
			t.Fatal(err)
		}

		agent := document.Workflow.Spec.Agents["worker"]
		agent.MayCommit = true
		document.Workflow.Spec.Agents["worker"] = agent
		err := newCapabilityEngine(t, document.Workflow, repo, &capabilityActionProvider{}).Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "executable workflow definition changed") {
			t.Fatalf("changed commit authority error = %v", err)
		}
	})

	t.Run("runtime checkpoint accepts allowed work when actor commits are disabled", func(t *testing.T) {
		repo := newDurableRepo(t)
		document := decodeV1Alpha2CapabilityDocument(t, "runner: test, model: capability-model, may_commit: false", "test -f allowed.txt")
		providerImpl := &capabilityActionProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "allowed.txt"), []byte("accepted\n"), 0o644)
		}}
		e := newCapabilityEngine(t, document.Workflow, repo, providerImpl)
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if providerImpl.calls != 1 {
			t.Fatalf("actor calls = %d, want 1", providerImpl.calls)
		}
		phase, err := e.phaseByID("work")
		if err != nil {
			t.Fatal(err)
		}
		if ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil || !ok {
			t.Fatalf("runtime checkpoint did not accept allowed work: ok=%v err=%v", ok, err)
		}
	})

	t.Run("commit authority cannot escape the workspace allowlist", func(t *testing.T) {
		repo := newDurableRepo(t)
		document := decodeV1Alpha2CapabilityDocument(t, "runner: test, model: capability-model, may_commit: true", "true")
		document.Workflow.Spec.Workspace.MutationPolicy.Allowed = []string{"allowed.txt"}
		providerImpl := &capabilityActionProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "outside.txt"), []byte("blocked\n"), 0o644)
		}}
		e := newCapabilityEngine(t, document.Workflow, repo, providerImpl)
		err := e.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "out-of-scope file changed: outside.txt") {
			t.Fatalf("out-of-scope mutation error = %v", err)
		}
		assertNoCapabilityPhaseMarker(t, e, "work")
	})

	t.Run("provider output and presentation capabilities do not waive validation or recovery evidence", func(t *testing.T) {
		repo := newDurableRepo(t)
		document := decodeV1Alpha2CapabilityDocument(t,
			"runner: test, model: capability-model, sandbox: workspace-write, approval: never, ephemeral: true, output_last_message: true, may_commit: true",
			"false",
		)
		firstProvider := &capabilityActionProvider{result: provider.Result{FinalMessage: "the phase is complete and accepted"}, action: func(_ context.Context, request provider.Request) error {
			if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("actor work\n"), 0o644); err != nil {
				return err
			}
			gitIn(t, request.Workspace, "add", "work.txt")
			gitIn(t, request.Workspace, "commit", "-qm", "actor-created commit")
			return nil
		}}
		first := newCapabilityEngine(t, document.Workflow, repo, firstProvider)
		err := first.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "validation") {
			t.Fatalf("validation-bypass error = %v", err)
		}
		assertNoCapabilityPhaseMarker(t, first, "work")
		var active ActivePhase
		if ok, err := first.Store.GetJSON(first.activeRecord(), &active); err != nil || !ok || !active.ActorCompleted {
			t.Fatalf("durable actor evidence = %#v ok=%v err=%v", active, ok, err)
		}

		resumedProvider := &capabilityActionProvider{result: provider.Result{FinalMessage: "presentation must not matter"}}
		resumed := newCapabilityEngine(t, document.Workflow, repo, resumedProvider)
		err = resumed.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "validation") {
			t.Fatalf("recovery validation error = %v", err)
		}
		if resumedProvider.calls != 0 {
			t.Fatalf("recovery replayed actor from provider output: calls=%d", resumedProvider.calls)
		}
		assertNoCapabilityPhaseMarker(t, resumed, "work")
	})
}

func TestV1Alpha2MaterialAgentCapabilitiesChangeRunIdentity(t *testing.T) {
	repo := newDurableRepo(t)
	document := decodeV1Alpha2CapabilityDocument(t,
		"runner: codex, model: capability-model, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true",
		"true",
	)
	base := newCapabilityEngine(t, document.Workflow, repo, &capabilityActionProvider{})
	want, err := base.expectedRunIdentity()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		change func(*workflow.Agent)
	}{
		{name: "runner", change: func(agent *workflow.Agent) { agent.Runner = "other" }},
		{name: "model", change: func(agent *workflow.Agent) { agent.Model = "other-model" }},
		{name: "sandbox", change: func(agent *workflow.Agent) { agent.Sandbox = "danger-full-access" }},
		{name: "approval", change: func(agent *workflow.Agent) { agent.Approval = "on-request" }},
		{name: "ephemeral", change: func(agent *workflow.Agent) { agent.Ephemeral = false }},
		{name: "may_commit", change: func(agent *workflow.Agent) { agent.MayCommit = false }},
		{name: "output_last_message", change: func(agent *workflow.Agent) { agent.OutputLastMessage = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			modified := *document.Workflow
			modified.Spec.Agents = make(map[string]workflow.Agent, len(document.Workflow.Spec.Agents))
			for name, agent := range document.Workflow.Spec.Agents {
				modified.Spec.Agents[name] = agent
			}
			agent := modified.Spec.Agents["worker"]
			test.change(&agent)
			modified.Spec.Agents["worker"] = agent

			candidate := newCapabilityEngine(t, &modified, repo, &capabilityActionProvider{})
			got, err := candidate.expectedRunIdentity()
			if err != nil {
				t.Fatal(err)
			}
			if got.WorkflowDigest == want.WorkflowDigest {
				t.Fatalf("capability change did not change workflow identity: %#v", got)
			}
		})
	}
}

func newCapabilityEngine(t *testing.T, w *workflow.Workflow, repo string, p provider.Provider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func assertNoCapabilityPhaseMarker(t *testing.T, e *Engine, id string) {
	t.Helper()
	phase, err := e.phaseByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("phase %q marker accepted: ok=%v err=%v", id, ok, err)
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
