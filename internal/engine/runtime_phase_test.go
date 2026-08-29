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

func TestRunAgentPrependsNeutralRuntimeExecutionBoundary(t *testing.T) {
	providerImpl := &presentationRecordingProvider{}
	p := &workflow.Phase{
		ID:              "implement",
		Kind:            "criterion",
		Actor:           "worker",
		Validation:      "phaseGate",
		AdvanceProgress: true,
	}
	e := &Engine{
		Workflow: &workflow.Workflow{Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{MutationPolicy: workflow.MutationPolicy{
				Allowed: []string{"src/**", "docs/*.md"},
				Integrity: []workflow.IntegrityRule{
					{
						ID:                     "roadmap-and-rules-governance",
						Paths:                  []string{"data/mothership/v1.2/**"},
						Exclude:                []string{"data/mothership/v1.2/generated/**"},
						Mode:                   "normalized-hash",
						AllowedSemanticChanges: []string{"criterion checkbox state"},
					},
					{
						ID:    "runtime-control",
						Paths: []string{".agentflow/workflows/task.yaml", ".agents/skills/agentflow-spec"},
						Mode:  "exact-hash",
					},
				},
			}},
			Progress: workflow.ProgressSpec{Source: workflow.ProgressSource{Path: "docs/roadmap.md"}},
			Agents: map[string]workflow.Agent{
				"worker": {Runner: "test", MayCommit: false},
			},
		}},
		Providers: map[string]provider.Provider{"test": providerImpl},
		Repo:      gitstate.Repo{Root: newDurableRepo(t)},
	}

	const authoredPrompt = "Implement only the selected roadmap criterion."
	if err := e.runAgent(context.Background(), "worker", "high", authoredPrompt, p); err != nil {
		t.Fatal(err)
	}
	prompt := providerImpl.request.Prompt
	if !strings.HasPrefix(prompt, "Runtime-enforced execution boundary:\n") {
		t.Fatalf("provider prompt does not start with runtime boundary:\n%s", prompt)
	}
	for _, want := range []string{
		"writable path patterns:\n  - \"src/**\"\n  - \"docs/*.md\"",
		"protected path patterns:\n  - \"data/mothership/v1.2/**\" [excludes=[\"data/mothership/v1.2/generated/**\"]]",
		"runtime-owned progress files (do not edit):\n  - \"docs/roadmap.md\"",
		"commit authority: forbidden; do not create commits",
		"required checks:\n  - \"phaseGate\"",
		"\nTask:\n" + authoredPrompt,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("provider prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "allowed_semantic_changes") {
		t.Fatalf("provider prompt advertises unenforced semantic changes:\n%s", prompt)
	}
	for _, unwanted := range []string{"agentflow", "workflow"} {
		if strings.Contains(strings.ToLower(prompt), unwanted) {
			t.Fatalf("runtime prompt exposes implementation-specific reference %q:\n%s", unwanted, prompt)
		}
	}
}

func TestRunAgentRemapsExpandedAuthoritativeWorkspacePathsIntoQuarantine(t *testing.T) {
	repo := newDurableRepo(t)
	unrelated := t.TempDir()
	providerImpl := &capabilityRecordingProvider{}
	p := &workflow.Phase{
		ID:              "implement",
		Kind:            "implementation",
		Actor:           "worker",
		AdvanceProgress: true,
	}
	e := &Engine{
		Workflow: &workflow.Workflow{Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt"}}},
			Progress:  workflow.ProgressSpec{Source: workflow.ProgressSource{Path: "{{ parameters.repo_root }}/progress.md"}},
			Agents: map[string]workflow.Agent{
				"worker": {Runner: "test", Model: "{{ parameters.repo_root }}/models/actor"},
			},
		}},
		Parameters: map[string]any{"repo_root": repo},
		Providers:  map[string]provider.Provider{"test": providerImpl},
		Repo:       gitstate.Repo{Root: repo},
	}

	prompt := fmt.Sprintf(
		"Edit {{ parameters.repo_root }}/work.txt, but leave the unrelated absolute path %s unchanged.",
		filepath.Join(unrelated, "reference.txt"),
	)
	if err := e.runAgent(context.Background(), "worker", "high", prompt, p); err != nil {
		t.Fatal(err)
	}

	request := providerImpl.request
	if request.Workspace == repo {
		t.Fatal("provider received the authoritative workspace")
	}
	for label, value := range map[string]string{
		"workspace": request.Workspace,
		"model":     request.Model,
		"prompt":    request.Prompt,
	} {
		if strings.Contains(strings.ToLower(value), "agentflow") {
			t.Fatalf("provider %s exposes implementation-specific reference: %q", label, value)
		}
	}
	if strings.Contains(request.Model, repo) {
		t.Fatalf("provider model leaked authoritative workspace %q: %q", repo, request.Model)
	}
	if strings.Contains(request.Prompt, repo) {
		t.Fatalf("provider prompt leaked authoritative workspace %q:\n%s", repo, request.Prompt)
	}
	for _, want := range []string{
		filepath.Join(request.Workspace, "models/actor"),
		filepath.Join(request.Workspace, "work.txt"),
		filepath.Join(request.Workspace, "progress.md"),
		filepath.Join(unrelated, "reference.txt"),
	} {
		if !strings.Contains(request.Model+"\n"+request.Prompt, want) {
			t.Fatalf("provider request did not preserve/remap %q: model=%q prompt=%q", want, request.Model, request.Prompt)
		}
	}
}

func TestRunAgentRemappedPromptCannotWriteAuthoritativeWorkspace(t *testing.T) {
	repo := newDurableRepo(t)
	authoritativeTarget := filepath.Join(repo, "blocked.txt")
	var providerRequest provider.Request
	providerImpl := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		providerRequest = request
		target := authoritativeTarget
		if !strings.Contains(request.Prompt, authoritativeTarget) {
			target = filepath.Join(request.Workspace, "blocked.txt")
		}
		return os.WriteFile(target, []byte("provider edit\n"), 0o644)
	}}
	w := durableWorkflow(repo, "remapped-prompt-cannot-bypass-quarantine")
	w.Spec.Phases[0].Prompt = "Write {{ parameters.repo_root }}/blocked.txt."
	e := newDurableEngine(t, w, providerImpl)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) {
		t.Fatalf("runAgent error = %v, want quarantined mutation-policy violation", err)
	}
	if strings.Contains(providerRequest.Prompt, authoritativeTarget) {
		t.Fatalf("provider prompt leaked authoritative target %q: %q", authoritativeTarget, providerRequest.Prompt)
	}
	if _, err := os.Stat(authoritativeTarget); !os.IsNotExist(err) {
		t.Fatalf("provider modified authoritative workspace through expanded prompt path: %v", err)
	}
}

func TestRemapWorkspacePathReferencesPreservesUnrelatedAbsolutePaths(t *testing.T) {
	authoritative := filepath.Join(string(filepath.Separator), "srv", "agentflow", "repository")
	quarantine := filepath.Join(string(filepath.Separator), "tmp", "agentflow-quarantine", "worktree")
	sibling := authoritative + "-archive"
	containing := filepath.Join(string(filepath.Separator), "mirror") + authoritative
	unrelated := filepath.Join(string(filepath.Separator), "opt", "reference", "asset.txt")

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "exact root", value: authoritative, want: quarantine},
		{
			name:  "descendant in prose",
			value: "edit " + filepath.Join(authoritative, "work.txt") + ", then validate",
			want:  "edit " + filepath.Join(quarantine, "work.txt") + ", then validate",
		},
		{
			name:  "multiple quoted references",
			value: fmt.Sprintf("%q and %q", authoritative, filepath.Join(authoritative, "nested", "work.txt")),
			want:  fmt.Sprintf("%q and %q", quarantine, filepath.Join(quarantine, "nested", "work.txt")),
		},
		{
			name:  "file URI",
			value: "file://" + filepath.Join(authoritative, "work.txt"),
			want:  "file://" + filepath.Join(quarantine, "work.txt"),
		},
		{name: "sibling prefix", value: sibling, want: sibling},
		{name: "root text inside another absolute path", value: containing, want: containing},
		{name: "unrelated absolute path", value: unrelated, want: unrelated},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := remapWorkspacePathReferences(test.value, authoritative, quarantine); got != test.want {
				t.Fatalf("remapWorkspacePathReferences(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestWriteProtectedPromptPatternsOmitsUnenforcedSemanticChanges(t *testing.T) {
	for _, mode := range []string{"exact-hash", "normalized-hash"} {
		t.Run(mode, func(t *testing.T) {
			var prompt strings.Builder
			writeProtectedPromptPatterns(&prompt, []workflow.IntegrityRule{{
				ID:                     "protected",
				Paths:                  []string{"protected/**"},
				Mode:                   mode,
				AllowedSemanticChanges: []string{"documented exception"},
			}})

			if strings.Contains(prompt.String(), "allowed_semantic_changes") {
				t.Fatalf("prompt advertises unenforced semantic changes:\n%s", prompt.String())
			}
		})
	}
}

func TestRunRepairAgentReceivesItsActualExecutionBoundary(t *testing.T) {
	providerImpl := &presentationRecordingProvider{}
	p := &workflow.Phase{
		ID:              "implement",
		Kind:            "criterion",
		Validation:      "phaseGate",
		AdvanceProgress: true,
	}
	e := &Engine{
		Workflow: &workflow.Workflow{Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{MutationPolicy: workflow.MutationPolicy{Allowed: []string{"work.txt"}}},
			Progress:  workflow.ProgressSpec{Source: workflow.ProgressSource{Path: "roadmap.md"}},
			Agents: map[string]workflow.Agent{
				"repair": {Runner: "test", MayCommit: true},
			},
		}},
		Providers: map[string]provider.Provider{"test": providerImpl},
		Repo:      gitstate.Repo{Root: newDurableRepo(t)},
	}

	if err := e.runRepairAgent(context.Background(), "repair", "high", "Repair the bounded failure.", "repairGate", p); err != nil {
		t.Fatal(err)
	}
	prompt := providerImpl.request.Prompt
	for _, want := range []string{
		"commit authority: allowed; commits created by this actor are permitted but do not establish acceptance",
		"required checks:\n  - \"repairGate\"",
		"runtime-owned progress files (do not edit):\n  - \"roadmap.md\"",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("repair prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "\"phaseGate\"") {
		t.Fatalf("repair prompt reported the phase gate instead of its selected repair gate:\n%s", prompt)
	}
}

func TestRunAgentReportsLegacyProceduralValidationBoundary(t *testing.T) {
	providerImpl := &presentationRecordingProvider{}
	p := &workflow.Phase{
		ID:    "legacy",
		Actor: "worker",
		After: []workflow.PhaseAction{{Validate: "legacyGate"}},
	}
	e := &Engine{
		Workflow: &workflow.Workflow{Spec: workflow.Spec{Agents: map[string]workflow.Agent{
			"worker": {Runner: "test"},
		}}},
		Providers: map[string]provider.Provider{"test": providerImpl},
		Repo:      gitstate.Repo{Root: newDurableRepo(t)},
	}

	if err := e.runAgent(context.Background(), "worker", "", "Do legacy work.", p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(providerImpl.request.Prompt, "required checks:\n  - \"legacyGate\"") {
		t.Fatalf("legacy provider prompt = %s", providerImpl.request.Prompt)
	}
}

func TestRunAgentLeavesEmptySandboxProviderNeutralForInjectedProvider(t *testing.T) {
	providerImpl := &capabilityRecordingProvider{}
	e := &Engine{
		Workflow: &workflow.Workflow{Spec: workflow.Spec{Agents: map[string]workflow.Agent{
			"worker": {Runner: "custom"},
		}}},
		Providers: map[string]provider.Provider{"custom": providerImpl},
		Repo:      gitstate.Repo{Root: newDurableRepo(t)},
	}

	if err := e.runAgent(context.Background(), "worker", "", "do work", nil); err != nil {
		t.Fatal(err)
	}
	if providerImpl.request.Sandbox != "" {
		t.Fatalf("injected provider sandbox = %q, want provider-neutral empty value", providerImpl.request.Sandbox)
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

func TestRunAgentUsesEffectiveActorCommitPermission(t *testing.T) {
	newV1Alpha1Workflow := func(agents map[string]workflow.Agent, workspace workflow.WorkspaceSpec) *workflow.Workflow {
		return &workflow.Workflow{
			APIVersion: "agentflow.dev/v1alpha1",
			Metadata:   workflow.Metadata{Name: "actor-commit-permission"},
			Spec: workflow.Spec{
				Workspace: workspace,
				Agents:    agents,
			},
		}
	}

	tests := []struct {
		name          string
		actorName     string
		buildWorkflow func(t *testing.T) *workflow.Workflow
		wantAllowed   bool
	}{
		{
			name:      "v1alpha1 actor may commit",
			actorName: "worker",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: true},
				}, workflow.WorkspaceSpec{})
			},
			wantAllowed: true,
		},
		{
			name:      "v1alpha1 workspace agent commits allows actor",
			actorName: "worker",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: false},
				}, workflow.WorkspaceSpec{AgentCommits: workflow.AgentCommits{Allowed: true}})
			},
			wantAllowed: true,
		},
		{
			name:      "v1alpha1 checkpoint agent commits allows actor",
			actorName: "worker",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: false},
				}, workflow.WorkspaceSpec{Checkpointing: workflow.CheckpointSpec{AgentCommitsAllowed: true}})
			},
			wantAllowed: true,
		},
		{
			name:      "all actor commit permissions false",
			actorName: "worker",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: false},
				}, workflow.WorkspaceSpec{})
			},
		},
		{
			name:      "v1alpha2 may commit true",
			actorName: "worker",
			buildWorkflow: func(t *testing.T) *workflow.Workflow {
				return decodeV1Alpha2CapabilityDocument(t, "runner: test, model: capability-model, may_commit: true", "true").Workflow
			},
			wantAllowed: true,
		},
		{
			name:      "v1alpha2 may commit false",
			actorName: "worker",
			buildWorkflow: func(t *testing.T) *workflow.Workflow {
				return decodeV1Alpha2CapabilityDocument(t, "runner: test, model: capability-model, may_commit: false", "true").Workflow
			},
		},
		{
			name:      "primary actor cannot borrow repair may commit",
			actorName: "worker",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: false},
					"repair": {Runner: "test", MayCommit: true},
				}, workflow.WorkspaceSpec{})
			},
		},
		{
			name:      "repair actor uses its own may commit",
			actorName: "repair",
			buildWorkflow: func(*testing.T) *workflow.Workflow {
				return newV1Alpha1Workflow(map[string]workflow.Agent{
					"worker": {Runner: "test", MayCommit: false},
					"repair": {Runner: "test", MayCommit: true},
				}, workflow.WorkspaceSpec{})
			},
			wantAllowed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			providerImpl := &capabilityActionProvider{action: func(_ context.Context, request provider.Request) error {
				path := filepath.Join(request.Workspace, "actor-commit.txt")
				if err := os.WriteFile(path, []byte("committed\n"), 0o644); err != nil {
					return err
				}
				gitIn(t, request.Workspace, "add", "actor-commit.txt")
				gitIn(t, request.Workspace, "commit", "-qm", "actor-created commit")
				return nil
			}}
			e := &Engine{
				Workflow:  test.buildWorkflow(t),
				Repo:      gitstate.Repo{Root: repo},
				Providers: map[string]provider.Provider{"test": providerImpl},
			}

			err := e.runAgent(context.Background(), test.actorName, "", "do work", nil)
			var safetyErr *safetyViolation
			if errors.As(err, &safetyErr) != !test.wantAllowed {
				t.Fatalf("runAgent error = %v, safety violation = %t, want allowed=%t", err, errors.As(err, &safetyErr), test.wantAllowed)
			}
			if test.wantAllowed && err != nil {
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
		providerRequests[0].Workspace = ""
		providerRequests[1].Workspace = ""
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

func TestLifecycleCommitDefenseUsesWorkflowActorCommitPermission(t *testing.T) {
	tests := []struct {
		name        string
		workspace   workflow.WorkspaceSpec
		wantAllowed bool
	}{
		{
			name:        "workspace agent commits allowed",
			workspace:   workflow.WorkspaceSpec{AgentCommits: workflow.AgentCommits{Allowed: true}},
			wantAllowed: true,
		},
		{
			name:        "checkpoint agent commits allowed",
			workspace:   workflow.WorkspaceSpec{Checkpointing: workflow.CheckpointSpec{AgentCommitsAllowed: true}},
			wantAllowed: true,
		},
		{
			name:      "all actor commit permissions false",
			workspace: workflow.WorkspaceSpec{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			e := &Engine{
				Workflow: &workflow.Workflow{
					APIVersion: "agentflow.dev/v1alpha1",
					Metadata:   workflow.Metadata{Name: "lifecycle-actor-commit-permission"},
					Spec: workflow.Spec{
						Workspace: test.workspace,
						Agents: map[string]workflow.Agent{
							"worker": {Runner: "test", MayCommit: false},
						},
					},
				},
				Repo: gitstate.Repo{Root: repo},
			}
			start, err := e.Repo.Head()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, "actor-change.txt"), []byte("change\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "add", "actor-change.txt")
			gitIn(t, repo, "commit", "-qm", "actor change")

			err = e.assertAgentCommitPolicy(&workflow.Phase{ID: "work", Actor: "worker"}, ActivePhase{StartCommit: start})
			if test.wantAllowed {
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
