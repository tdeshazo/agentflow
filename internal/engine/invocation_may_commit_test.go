package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestInvocationScopedMayCommitPrimaryAndRepairAuthorities(t *testing.T) {
	tests := []struct {
		name              string
		primaryMayCommit  bool
		repairMayCommit   bool
		wantSuccess       bool
		wantRepairAttempt bool
	}{
		{
			name:              "primary true repair false is rejected",
			primaryMayCommit:  true,
			repairMayCommit:   false,
			wantRepairAttempt: true,
		},
		{
			name:              "primary false repair true is accepted",
			primaryMayCommit:  false,
			repairMayCommit:   true,
			wantSuccess:       true,
			wantRepairAttempt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := durableWorkflow(repo, "invocation-authority-"+strings.ReplaceAll(tt.name, " ", "-"))
			worker := w.Spec.Agents["worker"]
			worker.MayCommit = tt.primaryMayCommit
			w.Spec.Agents["worker"] = worker
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: tt.repairMayCommit}
			w.Spec.Validation["phaseGate"] = repairValidation()

			p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
				path := filepath.Join(request.Workspace, "work.txt")
				if request.Metadata["actor"] == "repair" {
					if err := os.WriteFile(path, []byte("complete\n"), 0o644); err != nil {
						return err
					}
					gitIn(t, request.Workspace, "add", "work.txt")
					gitIn(t, request.Workspace, "commit", "-qm", "repair commit")
					return nil
				}
				if tt.primaryMayCommit {
					if err := os.WriteFile(path, []byte("partial\n"), 0o644); err != nil {
						return err
					}
					gitIn(t, request.Workspace, "add", "work.txt")
					gitIn(t, request.Workspace, "commit", "-qm", "primary partial commit")
					return nil
				}
				return os.WriteFile(path, []byte("partial\n"), 0o644)
			}}

			e := newDurableEngine(t, w, p)
			err := e.Run(context.Background())
			if tt.wantSuccess {
				if err != nil {
					t.Fatal(err)
				}
				assertDurableCompletion(t, e, repo)
				return
			}
			var safetyErr *safetyViolation
			if !errors.As(err, &safetyErr) {
				t.Fatalf("rejected run error = %v, want safety violation", err)
			}
			assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
			var active ActivePhase
			if ok, readErr := e.Store.GetJSON(e.activeRecord(), &active); readErr != nil || !ok || active.FailureKind != PhaseFailureSafety {
				t.Fatalf("safety state = %+v ok=%v err=%v", active, ok, readErr)
			}
			if got := active.RepairAttempts["phaseGate"]; (got == 0) != !tt.wantRepairAttempt {
				t.Fatalf("repair attempts = %d, want attempted=%t", got, tt.wantRepairAttempt)
			}
		})
	}
}

func TestValidationRepairErrorAfterUnauthorizedCommitIsSafetyFailure(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "repair-error-after-commit")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Validation["phaseGate"] = repairValidation()
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] != "repair" {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
		}
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "unauthorized repair commit")
		return errors.New("repair provider failed after commit")
	}}
	e := newDurableEngine(t, w, p)
	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), `actor "repair"`) {
		t.Fatalf("error = %v, want repair safety violation", err)
	}
	assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
	var active ActivePhase
	if ok, readErr := e.Store.GetJSON(e.activeRecord(), &active); readErr != nil || !ok || active.FailureKind != PhaseFailureSafety {
		t.Fatalf("safety state = %+v ok=%v err=%v", active, ok, readErr)
	}
}

func TestCompletionValidationUsesRepairInvocationAuthority(t *testing.T) {
	apiVersions := []string{"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2"}
	tests := []struct {
		name            string
		repairMayCommit bool
		wantSafety      bool
	}{
		{name: "authorized completion repair", repairMayCommit: true},
		{name: "unauthorized completion repair fails closed", repairMayCommit: false, wantSafety: true},
	}
	for _, apiVersion := range apiVersions {
		t.Run(apiVersion, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					repo := newDurableRepo(t)
					w := completionRepairWorkflow(repo, "completion-authority-"+strings.ReplaceAll(tt.name, " ", "-"))
					w.APIVersion = apiVersion
					w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "worker-model", MayCommit: false}
					w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model", MayCommit: tt.repairMayCommit}
					p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
						if request.Metadata["actor"] != "repair" {
							return nil
						}
						if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("done\n"), 0o644); err != nil {
							return err
						}
						gitIn(t, request.Workspace, "add", "completion.txt")
						gitIn(t, request.Workspace, "commit", "-qm", "completion repair commit")
						return nil
					}}
					e := newSchedulingEngine(t, w, p)
					if err := e.initializeState(); err != nil {
						t.Fatal(err)
					}
					failureRecord := e.standaloneFailureRecordForScope("completion/default/final")
					e.completionValidation = "outer"
					err := e.runCompletionValidation(context.Background(), "default", "final")
					if e.completionValidation != "outer" {
						t.Fatalf("completion validation scope was not restored: %q", e.completionValidation)
					}
					e.completionValidation = ""
					if tt.wantSafety {
						var safetyErr *safetyViolation
						if !errors.As(err, &safetyErr) {
							t.Fatalf("completion repair error = %v, want safety violation", err)
						}
						assertNoDurablePhaseOrCompletionMarkers(t, e, "root")
						var failure validationFailureEvidence
						ok, readErr := e.Store.GetJSON(failureRecord, &failure)
						if readErr != nil || !ok || failure.FailureKind != PhaseFailureSafety {
							t.Fatalf("completion safety evidence = %+v ok=%v err=%v", failure, ok, readErr)
						}
						if err := e.runToolUses(context.Background(), w.Spec.Validation["final"].Steps, nil); err != nil {
							t.Fatalf("final deterministic validation after rejected repair = %v", err)
						}
						gitIn(t, repo, "revert", "--no-edit", "HEAD")
						if err := os.WriteFile(filepath.Join(repo, "completion.txt"), []byte("done\n"), 0o644); err != nil {
							t.Fatal(err)
						}
						gitIn(t, repo, "add", "completion.txt")
						gitIn(t, repo, "commit", "-qm", "manual completion remediation")
						restarted := newSchedulingEngine(t, w, p)
						if err := restarted.Run(context.Background()); !errors.As(err, &safetyErr) {
							t.Fatalf("restarted workflow error = %v, want durable safety failure", err)
						}
						if pCalls := len(p.calls); pCalls != 1 {
							t.Fatalf("restart invoked a repair actor: calls = %d", pCalls)
						}
						assertNoDurablePhaseOrCompletionMarkers(t, e, "root")
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, ok, err := e.Store.Resolve(failureRecord); err != nil || ok {
						t.Fatalf("successful completion repair left failure evidence: ok=%v err=%v", ok, err)
					}
				})
			}
		})
	}
}

func TestMayCommitFalseActorCanUseRuntimeCheckpointForAllowedDirtyWork(t *testing.T) {
	repo := newDurableRepo(t)
	w := compactLifecycleWorkflow(repo, "runtime-checkpoint-authority")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDurableCompletion(t, e, repo)
	if !strings.Contains(gitIn(t, repo, "log", "--format=%s"), "checkpoint:") {
		t.Fatalf("runtime checkpoint commit missing from history")
	}
}

func TestValidationRepairMutationSafetyBecomesTerminalImmediately(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "repair-mutation-safety")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Validation["phaseGate"] = repairValidation()
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] == "repair" {
			return os.WriteFile(filepath.Join(request.Workspace, "not-allowed.txt"), []byte("unsafe\n"), 0o644)
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	var safetyErr *safetyViolation
	if err := e.Run(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("repair mutation error = %v, want safety violation", err)
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.FailureKind != PhaseFailureSafety {
		t.Fatalf("repair mutation safety state = %+v ok=%t err=%v", active, ok, err)
	}
	if p.calls != 2 {
		t.Fatalf("provider calls = %d, want primary plus repair", p.calls)
	}
	if err := newDurableEngine(t, w, p).Run(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("restart error = %v, want durable safety violation", err)
	}
	if p.calls != 2 {
		t.Fatalf("durable repair mutation replayed an actor: calls=%d", p.calls)
	}
}

func TestCompletionRepairBudgetSurvivesBeforeCompletionMarker(t *testing.T) {
	for _, apiVersion := range []string{"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2"} {
		t.Run(apiVersion, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := completionRepairWorkflow(repo, "completion-repair-budget")
			w.APIVersion = apiVersion
			w.Spec.Flow = []workflow.FlowStep{{Complete: "default"}}
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model"}
			p := &schedulingProvider{action: func(_ context.Context, request provider.Request) error {
				if request.Metadata["actor"] == "repair" {
					return os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("done\n"), 0o644)
				}
				return nil
			}}
			e := newSchedulingEngine(t, w, p)
			if err := e.initializeState(); err != nil {
				t.Fatal(err)
			}
			if err := e.runPhase(context.Background(), "root"); err != nil {
				t.Fatal(err)
			}
			if err := e.runCompletionValidation(context.Background(), "default", "final"); err != nil {
				t.Fatal(err)
			}
			repairRecord := fmt.Sprintf("validation-repairs/%x", "completion/default/final")
			if _, ok, err := e.Store.Resolve(repairRecord); err != nil || !ok {
				t.Fatalf("completion repair budget was cleared before completion marker: ok=%t err=%v", ok, err)
			}
			if err := os.Remove(filepath.Join(repo, "completion.txt")); err != nil {
				t.Fatal(err)
			}
			restarted := newSchedulingEngine(t, w, p)
			if err := restarted.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
				t.Fatalf("restart error = %v, want exhausted repair budget", err)
			}
			if len(p.calls) != 2 {
				t.Fatalf("restart granted another completion repair attempt: calls=%d", len(p.calls))
			}
		})
	}
}

func TestV1Alpha1CompletionRecognizesLegacyUnscopedValidationState(t *testing.T) {
	t.Run("consumed repair budget remains consumed after restart", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := completionRepairWorkflow(repo, "legacy-completion-repair-budget")
		w.APIVersion = "agentflow.dev/v1alpha1"
		w.Spec.Flow = []workflow.FlowStep{{Complete: "default"}}
		p := &schedulingProvider{}

		preUpgrade := newSchedulingEngine(t, w, p)
		if err := preUpgrade.initializeState(); err != nil {
			t.Fatal(err)
		}
		legacyRecord := preUpgrade.standaloneRepairRecordForScope("final")
		if err := preUpgrade.Store.SetJSON(legacyRecord, standaloneRepairState{Attempts: 1}); err != nil {
			t.Fatalf("construct legacy repair state: %v", err)
		}

		restarted := newSchedulingEngine(t, w, p)
		err := restarted.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("restart error = %v, want legacy repair budget exhaustion", err)
		}
		if len(p.calls) != 0 {
			t.Fatalf("restart granted a repair attempt: calls=%d", len(p.calls))
		}
		var migrated standaloneRepairState
		if ok, err := restarted.Store.GetJSON(restarted.standaloneRepairRecordForScope("completion/default/final"), &migrated); err != nil || !ok || migrated.Attempts != 1 {
			t.Fatalf("migrated repair state = %+v ok=%t err=%v", migrated, ok, err)
		}
		var legacy standaloneRepairState
		if ok, err := restarted.Store.GetJSON(legacyRecord, &legacy); err != nil || !ok || legacy.Attempts != 1 {
			t.Fatalf("legacy repair state was not retained: %+v ok=%t err=%v", legacy, ok, err)
		}

		again := newSchedulingEngine(t, w, p)
		err = again.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("second restart error = %v, want legacy repair budget exhaustion", err)
		}
		if len(p.calls) != 0 {
			t.Fatalf("idempotent migration granted a repair attempt: calls=%d", len(p.calls))
		}
		if ok, err := again.Store.GetJSON(again.standaloneRepairRecordForScope("completion/default/final"), &migrated); err != nil || !ok || migrated.Attempts != 1 {
			t.Fatalf("idempotent migrated repair state = %+v ok=%t err=%v", migrated, ok, err)
		}
	})

	t.Run("legacy safety evidence remains terminal after restart", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := completionRepairWorkflow(repo, "legacy-completion-safety")
		w.APIVersion = "agentflow.dev/v1alpha1"
		w.Spec.Flow = []workflow.FlowStep{{Complete: "default"}}
		p := &schedulingProvider{}

		preUpgrade := newSchedulingEngine(t, w, p)
		if err := preUpgrade.initializeState(); err != nil {
			t.Fatal(err)
		}
		head, err := preUpgrade.Repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		legacyRecord := preUpgrade.standaloneFailureRecordForScope("final")
		legacyFailure := validationFailureEvidence{
			Validation:  "final",
			FailureKind: PhaseFailureSafety,
			Output:      "legacy bounded safety diagnostic",
			Actor:       "legacy-repair",
			Commit:      head,
		}
		if err := preUpgrade.Store.SetJSON(legacyRecord, legacyFailure); err != nil {
			t.Fatalf("construct legacy safety state: %v", err)
		}

		// This models a restart which enters the newly scoped completion gate
		// directly, rather than relying only on Run's global terminal-safety
		// scan to find the old unscoped record.
		scopedRestart := newSchedulingEngine(t, w, p)
		if err := scopedRestart.initializeOrResumeState(); err != nil {
			t.Fatal(err)
		}
		err = scopedRestart.runCompletionValidation(context.Background(), "default", "final")
		var safetyErr *safetyViolation
		if !errors.As(err, &safetyErr) {
			t.Fatalf("scoped restart error = %v, want durable safety violation", err)
		}
		if safetyErr.actor != legacyFailure.Actor || safetyErr.commit != legacyFailure.Commit || !strings.Contains(err.Error(), legacyFailure.Output) {
			t.Fatalf("safety attribution changed: %+v, error=%v", safetyErr, err)
		}

		restarted := newSchedulingEngine(t, w, p)
		err = restarted.Run(context.Background())
		if !errors.As(err, &safetyErr) {
			t.Fatalf("full restart error = %v, want durable safety violation", err)
		}
		if len(p.calls) != 0 {
			t.Fatalf("restart ran a repair after legacy safety failure: calls=%d", len(p.calls))
		}
		var retained validationFailureEvidence
		if ok, err := restarted.Store.GetJSON(legacyRecord, &retained); err != nil || !ok || retained != legacyFailure {
			t.Fatalf("legacy safety evidence changed: %+v ok=%t err=%v", retained, ok, err)
		}
		if _, ok, err := restarted.Store.Resolve(restarted.standaloneFailureRecordForScope("completion/default/final")); err != nil || ok {
			t.Fatalf("legacy safety evidence was rewritten into completion scope: ok=%t err=%v", ok, err)
		}
	})
}

func TestCompletionMarkerClearsCompletionRepairBudget(t *testing.T) {
	for _, apiVersion := range []string{"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2"} {
		t.Run(apiVersion, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := completionRepairWorkflow(repo, "completion-repair-cleanup")
			w.APIVersion = apiVersion
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model", MayCommit: true}
			p := &schedulingProvider{action: func(_ context.Context, request provider.Request) error {
				if request.Metadata["actor"] != "repair" {
					return nil
				}
				if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				gitIn(t, request.Workspace, "add", "completion.txt")
				gitIn(t, request.Workspace, "commit", "-qm", "completion repair")
				return nil
			}}
			e := newSchedulingEngine(t, w, p)
			if err := e.initializeState(); err != nil {
				t.Fatal(err)
			}
			if err := e.runCompletion(context.Background(), "default"); err != nil {
				t.Fatal(err)
			}
			if ok, _, err := e.validCommitMarker(e.workflowCompleteMarker()); err != nil || !ok {
				t.Fatalf("completion marker: ok=%t err=%v", ok, err)
			}
			repairRecord := fmt.Sprintf("validation-repairs/%x", "completion/default/final")
			if _, ok, err := e.Store.Resolve(repairRecord); err != nil || ok {
				t.Fatalf("completion repair budget remained after completion marker: ok=%t err=%v", ok, err)
			}
		})
	}
}

func assertNoDurablePhaseOrCompletionMarkers(t *testing.T, e *Engine, phaseID string) {
	t.Helper()
	p, err := e.phaseByID(phaseID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil || ok {
		t.Fatalf("phase marker accepted: ok=%v err=%v", ok, err)
	}
	if ok, _, err := e.validCommitMarker(e.workflowCompleteMarker()); err != nil || ok {
		t.Fatalf("completion marker accepted: ok=%v err=%v", ok, err)
	}
}

func completionRepairWorkflow(repo, name string) *workflow.Workflow {
	w := schedulingWorkflow(repo, name, []string{"root"}, nil, "true")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"*"}
	w.Spec.Validation["final"] = workflow.Validation{
		Steps: []workflow.ToolUse{{Uses: "final"}},
		OnFailure: workflow.FailurePolicy{
			Strategy:          "repair-once",
			MaxRepairAttempts: 1,
			Repair:            workflow.Repair{Actor: "repair", Prompt: "repair completion"},
		},
	}
	w.Spec.Tools["final"] = workflow.Tool{Type: "shell", Command: "test -f completion.txt"}
	w.Spec.Completion["default"] = workflow.Completion{FinalValidation: "final"}
	return w
}

func TestRepairSafetyFailureDoesNotGrantAnotherRepairAttempt(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "repair-safety-budget")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Validation["phaseGate"] = repairValidation()
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] != "repair" {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "safety attempt")
		return nil
	}}
	e := newDurableEngine(t, w, p)
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("unauthorized repair commit unexpectedly succeeded")
	}
	var active ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.RepairAttempts["phaseGate"] != 1 {
		t.Fatalf("repair budget after safety failure = %+v ok=%v err=%v", active, ok, err)
	}
	if p.calls != 2 {
		t.Fatalf("safety failure invoked %d actors, want primary plus one repair", p.calls)
	}
	assertNoDurablePhaseOrCompletionMarkers(t, e, "change")
}

func TestCommittedRepairCannotReleaseDependentPhase(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "committed-repair-does-not-release", []string{"root", "child"}, map[string][]string{"child": {"root"}}, "false")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"*"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "worker-model", MayCommit: false}
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model", MayCommit: true}
	gate := w.Spec.Validation["gate"]
	gate.OnFailure = workflow.FailurePolicy{
		Strategy:          "repair-once",
		MaxRepairAttempts: 1,
		Repair:            workflow.Repair{Actor: "repair", Prompt: "repair root"},
	}
	w.Spec.Validation["gate"] = gate
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "root" {
			return nil
		}
		if request.Metadata["actor"] == "repair" {
			if err := os.WriteFile(filepath.Join(request.Workspace, "root.txt"), []byte("repair commit\n"), 0o644); err != nil {
				return err
			}
			gitIn(t, request.Workspace, "add", "root.txt")
			gitIn(t, request.Workspace, "commit", "-qm", "repair root commit")
			return nil
		}
		return os.WriteFile(filepath.Join(request.Workspace, "root.txt"), []byte("partial\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, p)
	err := e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still fails after repair") {
		t.Fatalf("schedule error = %v, want failed post-repair validation", err)
	}
	assertSchedulingCalls(t, p, "root:worker", "root:repair")
	assertNoPhaseMarker(t, e, "root")
	assertNoPhaseMarker(t, e, "child")
	assertNoSchedulingCompletion(t, e)
}
