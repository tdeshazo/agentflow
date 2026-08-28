package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestV1Alpha1CompletionFinalValidationInitiallyPasses(t *testing.T) {
	repo := newDurableRepo(t)
	seedCompletionFile(t, repo)
	w := completionScopeRegressionWorkflow(repo, "v1alpha1-final-pass", "agentflow.dev/v1alpha1", "quality", false, "true")
	p := &completionRegressionProvider{}
	e := newCompletionRegressionEngine(t, w, p)

	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCompletionRegressionMarker(t, e, true)
	if len(p.calls) != 0 {
		t.Fatalf("repair actor calls = %d, want 0", len(p.calls))
	}
	assertCompletionRegressionRepairState(t, e, "default", "quality", 0, false)
}

func TestCompletionRetrySkipsInitializationPreconditionAndResolvesNamedAssertionTool(t *testing.T) {
	repo := newDurableRepo(t)
	statePath := filepath.Join(repo, "state.txt")
	if err := os.WriteFile(statePath, []byte("pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "state.txt")
	gitIn(t, repo, "commit", "-qm", "seed pending state")

	w := &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha1",
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: "completion-retry-initial-state"},
		Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{
				Root:           repo,
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"state.txt"}},
			},
			Preconditions: []workflow.Check{{
				ID: "initially-pending", Scope: "initialization", Type: "file-contains", Path: "state.txt", Text: "pending",
			}},
			Tools: map[string]workflow.Tool{
				"checked-state-with-an-arbitrary-name": {Type: "file-regex"},
			},
			Flow: []workflow.FlowStep{{Complete: "default"}},
			Completion: map[string]workflow.Completion{"default": {Assertions: []workflow.Assertion{{
				Uses: "checked-state-with-an-arbitrary-name",
				With: workflow.ToolArguments{Path: "state.txt", Regex: `(?m)^checked$`},
			}}}},
		},
	}

	first := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
	if err := first.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("initial completion error = %v, want regex failure", err)
	}
	if err := os.WriteFile(statePath, []byte("checked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "state.txt")
	gitIn(t, repo, "commit", "-qm", "advance mutable state")

	restarted := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatalf("completion retry: %v", err)
	}
	assertCompletionRegressionMarker(t, restarted, true)
}

func TestV1Alpha1CompletionFinalValidationRepairDurability(t *testing.T) {
	t.Run("repair succeeds and completion clears state after marker", func(t *testing.T) {
		repo := newDurableRepo(t)
		counter := filepath.Join(t.TempDir(), "validation-count")
		w := completionScopeRegressionWorkflow(repo, "v1alpha1-final-repair", "agentflow.dev/v1alpha1", "quality", true, completionRegressionCommand(counter))
		p := &completionRegressionProvider{action: func(_ context.Context, request provider.Request) error {
			if request.Metadata["actor"] == "repair" {
				if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("repaired\n"), 0o644); err != nil {
					return err
				}
				gitIn(t, request.Workspace, "add", "completion.txt")
				gitIn(t, request.Workspace, "commit", "-qm", "completion repair")
			}
			return nil
		}}
		e := newCompletionRegressionEngine(t, w, p)

		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(mustReadFile(t, counter)); got != 2 {
			t.Fatalf("deterministic validation calls = %d, want initial failure plus revalidation", got)
		}
		if got := strings.Join(p.calls, ","); got != "repair" {
			t.Fatalf("repair calls = %q, want exactly one repair", got)
		}
		assertCompletionRegressionMarker(t, e, true)
		assertCompletionRegressionRepairState(t, e, "default", "quality", 0, false)
	})

	t.Run("revalidation failure leaves consumed budget and blocks restart", func(t *testing.T) {
		repo := newDurableRepo(t)
		counter := filepath.Join(t.TempDir(), "validation-count")
		w := completionScopeRegressionWorkflow(repo, "v1alpha1-final-repair-fails", "agentflow.dev/v1alpha1", "quality", true, completionRegressionCommand(counter))
		p := &completionRegressionProvider{result: provider.Result{FinalMessage: "model says complete"}}
		e := newCompletionRegressionEngine(t, w, p)

		err := e.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "still fails after repair") {
			t.Fatalf("initial error = %v, want failed deterministic revalidation", err)
		}
		if len(p.calls) != 1 || len(mustReadFile(t, counter)) != 2 {
			t.Fatalf("initial failure work = repairs=%d validations=%d, want 1/2", len(p.calls), len(mustReadFile(t, counter)))
		}
		assertCompletionRegressionMarker(t, e, false)
		assertCompletionRegressionRepairState(t, e, "default", "quality", 1, true)

		restarted := newCompletionRegressionEngine(t, w, p)
		err = restarted.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
			t.Fatalf("restart error = %v, want exhausted repair budget", err)
		}
		if len(p.calls) != 1 || len(mustReadFile(t, counter)) != 3 {
			t.Fatalf("restart work = repairs=%d validations=%d, want 1/3", len(p.calls), len(mustReadFile(t, counter)))
		}
		assertCompletionRegressionMarker(t, restarted, false)
		assertCompletionRegressionRepairState(t, restarted, "default", "quality", 1, true)
	})
}

func TestV1Alpha1CompletionFinalValidationCrashAfterRepair(t *testing.T) {
	tests := []struct {
		name         string
		removeResult bool
		wantErr      bool
	}{
		{name: "restart failing gate cannot repair again", removeResult: true, wantErr: true},
		{name: "restart passing gate reruns validation and completes", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			counter := filepath.Join(t.TempDir(), "validation-count")
			w := completionScopeRegressionWorkflow(repo, "v1alpha1-crash-"+strings.ReplaceAll(tt.name, " ", "-"), "agentflow.dev/v1alpha1", "quality", true, completionRegressionCommand(counter))
			p := &completionRegressionProvider{action: func(_ context.Context, request provider.Request) error {
				if request.Metadata["actor"] == "repair" {
					if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("repaired\n"), 0o644); err != nil {
						return err
					}
					gitIn(t, request.Workspace, "add", "completion.txt")
					gitIn(t, request.Workspace, "commit", "-qm", "completion repair")
				}
				return nil
			}}
			first := newCompletionRegressionEngine(t, w, p)
			first.interruptionHook = func(point interruptionPoint, _ PendingActorInvocation) error {
				if point != interruptionBeforeCompletionMarker {
					return nil
				}
				assertCompletionRegressionMarker(t, first, false)
				assertCompletionRegressionRepairState(t, first, "default", "quality", 1, true)
				return errAdversarialInterruption
			}

			if err := first.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
				t.Fatalf("interrupted run error = %v", err)
			}
			if len(p.calls) != 1 || len(mustReadFile(t, counter)) != 2 {
				t.Fatalf("interrupted work = repairs=%d validations=%d, want 1/2", len(p.calls), len(mustReadFile(t, counter)))
			}
			if tt.removeResult {
				if err := os.Remove(filepath.Join(repo, "completion.txt")); err != nil {
					t.Fatal(err)
				}
				gitIn(t, repo, "add", "completion.txt")
				gitIn(t, repo, "commit", "-qm", "invalidate completion gate")
			}

			restarted := newCompletionRegressionEngine(t, w, p)
			err := restarted.Run(context.Background())
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
					t.Fatalf("restart error = %v, want exhausted repair budget", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(p.calls) != 1 || len(mustReadFile(t, counter)) != 3 {
				t.Fatalf("restart work = repairs=%d validations=%d, want 1/3", len(p.calls), len(mustReadFile(t, counter)))
			}
			assertCompletionRegressionMarker(t, restarted, !tt.wantErr)
			assertCompletionRegressionRepairState(t, restarted, "default", "quality", 1, tt.wantErr)
		})
	}
}

func TestV1Alpha1CompletionMarkerPrecedesRepairCleanup(t *testing.T) {
	repo := newDurableRepo(t)
	w := completionScopeRegressionWorkflow(repo, "v1alpha1-marker-order", "agentflow.dev/v1alpha1", "quality", true, "test -f completion.txt")
	p := &completionRegressionProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] == "repair" {
			if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("repaired\n"), 0o644); err != nil {
				return err
			}
			gitIn(t, request.Workspace, "add", "completion.txt")
			gitIn(t, request.Workspace, "commit", "-qm", "completion repair")
		}
		return nil
	}}
	e := newCompletionRegressionEngine(t, w, p)
	beforeCleanup := false
	e.interruptionHook = func(point interruptionPoint, _ PendingActorInvocation) error {
		if point != interruptionAfterCompletionMarker {
			return nil
		}
		beforeCleanup = true
		assertCompletionRegressionMarker(t, e, true)
		assertCompletionRegressionRepairState(t, e, "default", "quality", 1, true)
		return errAdversarialInterruption
	}

	if err := e.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
		t.Fatalf("interrupted completion error = %v", err)
	}
	if !beforeCleanup {
		t.Fatal("completion marker interruption seam was not reached")
	}
	if len(p.calls) != 1 {
		t.Fatalf("repair calls = %d, want 1", len(p.calls))
	}

	restarted := newCompletionRegressionEngine(t, w, p)
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("stale repair state caused duplicate work: calls=%d", len(p.calls))
	}
	assertCompletionRegressionMarker(t, restarted, true)
	// The marker is already accepted, so a stale transient record is harmless;
	// completion cleanup is not allowed to become a prerequisite for acceptance.
	assertCompletionRegressionRepairState(t, restarted, "default", "quality", 1, true)
}

func TestCompletionValidationScopesAreIsolated(t *testing.T) {
	t.Run("phase validation does not share completion budget", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := completionScopeRegressionWorkflow(repo, "phase-completion-scope", "agentflow.dev/v1alpha1", "quality", true, "false")
		w.Spec.Phases = []workflow.Phase{{ID: "phase", Kind: "implementation", Label: "phase", Validation: "quality"}}
		e := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		active, err := e.newActivePhase("phase")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
			t.Fatal(err)
		}
		if ok, err := e.consumeRepairAttempt("quality", mustPhase(t, e, "phase"), 1); err != nil || !ok {
			t.Fatalf("phase repair budget consumption: ok=%v err=%v", ok, err)
		}
		if err := e.runCompletionValidation(context.Background(), "default", "quality"); err == nil {
			t.Fatal("completion validation unexpectedly passed")
		}
		if len(e.Providers["test"].(*completionRegressionProvider).calls) != 1 {
			t.Fatal("completion validation did not receive its own repair attempt")
		}
		assertCompletionRegressionRepairState(t, e, "default", "quality", 1, true)
	})

	t.Run("ordinary flow validation remains unscoped", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := completionScopeRegressionWorkflow(repo, "ordinary-flow-scope", "agentflow.dev/v1alpha1", "quality", true, "false")
		e := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		if err := e.runValidation(context.Background(), "quality", nil); err == nil {
			t.Fatal("ordinary validation unexpectedly passed")
		}
		assertStandaloneRepairState(t, e, "quality", 1, true)
		assertCompletionRegressionRepairState(t, e, "default", "quality", 0, false)
	})

	t.Run("same validation name gets one budget per completion", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := completionScopeRegressionWorkflow(repo, "two-completions-scope", "agentflow.dev/v1alpha1", "quality", true, "false")
		w.Spec.Completion["second"] = workflow.Completion{FinalValidation: "quality"}
		p := &completionRegressionProvider{}
		e := newCompletionRegressionEngine(t, w, p)
		if err := e.initializeState(); err != nil {
			t.Fatal(err)
		}
		for _, completion := range []string{"default", "second"} {
			if err := e.runCompletionValidation(context.Background(), completion, "quality"); err == nil {
				t.Fatalf("completion %s unexpectedly passed", completion)
			}
			assertCompletionRegressionRepairState(t, e, completion, "quality", 1, true)
		}
		if len(p.calls) != 2 {
			t.Fatalf("repair calls = %d, want one per completion scope", len(p.calls))
		}
	})

	t.Run("v1alpha1 and v1alpha2 use equivalent completion scopes", func(t *testing.T) {
		for _, version := range []string{"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2"} {
			t.Run(version, func(t *testing.T) {
				repo := newDurableRepo(t)
				w := completionScopeRegressionWorkflow(repo, "cross-version-scope-"+strings.ReplaceAll(version, ".", "-"), version, "quality", true, "false")
				p := &completionRegressionProvider{}
				e := newCompletionRegressionEngine(t, w, p)
				if err := e.initializeState(); err != nil {
					t.Fatal(err)
				}
				if err := e.runCompletionValidation(context.Background(), "default", "quality"); err == nil {
					t.Fatal("completion unexpectedly passed")
				}
				if len(p.calls) != 1 {
					t.Fatalf("repair calls = %d, want 1", len(p.calls))
				}
				assertCompletionRegressionRepairState(t, e, "default", "quality", 1, true)
				if err := e.runCompletionValidation(context.Background(), "default", "quality"); err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
					t.Fatalf("second validation error = %v, want exhausted repair budget", err)
				}
				if len(p.calls) != 1 {
					t.Fatalf("second validation invoked repair: calls=%d", len(p.calls))
				}
			})
		}
	})
}

func TestV1Alpha1CompletionLegacyMigrationDoesNotResetAuthority(t *testing.T) {
	repo := newDurableRepo(t)
	w := completionScopeRegressionWorkflow(repo, "legacy-migration-interruption", "agentflow.dev/v1alpha1", "quality", true, "false")
	p := &completionRegressionProvider{result: provider.Result{FinalMessage: "accept"}}
	preUpgrade := newCompletionRegressionEngine(t, w, p)
	if err := preUpgrade.initializeState(); err != nil {
		t.Fatal(err)
	}
	legacyRecord := preUpgrade.standaloneRepairRecordForScope("quality")
	if err := preUpgrade.Store.SetJSON(legacyRecord, standaloneRepairState{Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	// A scoped zero-attempt record models an interruption after a weak or
	// partial migration write. Restart must strengthen it from legacy state.
	scopedRecord := preUpgrade.standaloneRepairRecordForScope("completion/default/quality")
	if err := preUpgrade.Store.SetJSON(scopedRecord, standaloneRepairState{}); err != nil {
		t.Fatal(err)
	}

	restarted := newCompletionRegressionEngine(t, w, p)
	err := restarted.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exhausted repair budget") {
		t.Fatalf("restart error = %v, want exhausted repair budget", err)
	}
	if len(p.calls) != 0 {
		t.Fatalf("migration restart invoked repair actor: calls=%d", len(p.calls))
	}
	assertCompletionRegressionRepairState(t, restarted, "default", "quality", 1, true)
	assertCompletionRegressionMarker(t, restarted, false)
}

func TestV1Alpha1CompletionLegacyMigrationRequiresCompatibleRunIdentity(t *testing.T) {
	repo := newDurableRepo(t)
	w := completionScopeRegressionWorkflow(repo, "legacy-migration-identity", "agentflow.dev/v1alpha1", "quality", true, "false")
	p := &completionRegressionProvider{}
	preUpgrade := newCompletionRegressionEngine(t, w, p)
	if err := preUpgrade.initializeState(); err != nil {
		t.Fatal(err)
	}
	if err := preUpgrade.Store.SetJSON(preUpgrade.standaloneRepairRecordForScope("quality"), standaloneRepairState{Attempts: 1}); err != nil {
		t.Fatal(err)
	}

	// A changed executable definition must stop the run before legacy state can
	// be copied into a new completion-scoped authority record.
	w.Spec.Tools["final"] = workflow.Tool{Type: "shell", Command: "true"}
	restarted := newCompletionRegressionEngine(t, w, p)
	err := restarted.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "executable workflow definition changed") {
		t.Fatalf("run identity error = %v", err)
	}
	if len(p.calls) != 0 {
		t.Fatalf("identity mismatch invoked repair actor: calls=%d", len(p.calls))
	}
	assertCompletionRegressionRepairState(t, restarted, "default", "quality", 0, false)
}

func TestV1Alpha1CompletionLegacyMigrationRejectsMalformedFailureState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		failure validationFailureEvidence
		want    string
	}{
		{
			name:    "mismatched validation",
			failure: validationFailureEvidence{Validation: "other", FailureKind: PhaseFailureValidation},
			want:    "mismatched validation",
		},
		{
			name:    "unknown failure kind",
			failure: validationFailureEvidence{Validation: "quality", FailureKind: PhaseFailureKind("unknown")},
			want:    "invalid failure kind",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			w := completionScopeRegressionWorkflow(repo, "legacy-migration-malformed-"+strings.ReplaceAll(tt.name, " ", "-"), "agentflow.dev/v1alpha1", "quality", true, "false")
			p := &completionRegressionProvider{}
			e := newCompletionRegressionEngine(t, w, p)
			if err := e.initializeState(); err != nil {
				t.Fatal(err)
			}
			if err := e.Store.SetJSON(e.standaloneFailureRecordForScope("quality"), tt.failure); err != nil {
				t.Fatal(err)
			}

			err := e.runCompletionValidation(context.Background(), "default", "quality")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("malformed legacy failure error = %v", err)
			}
			if len(p.calls) != 0 {
				t.Fatalf("malformed legacy failure invoked repair actor: calls=%d", len(p.calls))
			}
			assertCompletionRegressionRepairState(t, e, "default", "quality", 0, false)
		})
	}
}

type completionRegressionProvider struct {
	calls  []string
	action func(context.Context, provider.Request) error
	result provider.Result
}

func (p *completionRegressionProvider) Name() string { return "completion-regression" }

func (p *completionRegressionProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	p.calls = append(p.calls, request.Metadata["actor"])
	if p.action != nil {
		if err := p.action(ctx, request); err != nil {
			return provider.Result{}, err
		}
	}
	return p.result, nil
}

func completionScopeRegressionWorkflow(repo, name, version, validation string, repair bool, command string) *workflow.Workflow {
	policy := workflow.FailurePolicy{}
	if repair {
		policy = workflow.FailurePolicy{
			Strategy: "repair-once", MaxRepairAttempts: 1,
			Repair: workflow.Repair{Actor: "repair", Prompt: "repair final validation"},
		}
	}
	return &workflow.Workflow{
		APIVersion: version,
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: name},
		Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{Root: repo, MutationPolicy: workflow.MutationPolicy{Allowed: []string{"*"}}},
			Agents: map[string]workflow.Agent{
				"repair": {Runner: "test", Model: "repair-model", MayCommit: true},
			},
			Tools: map[string]workflow.Tool{
				"final": {Type: "shell", Command: command, MutatesWorkspace: strings.Contains(command, "validation-count")},
			},
			Validation: map[string]workflow.Validation{
				validation: {Steps: []workflow.ToolUse{{Uses: "final"}}, OnFailure: policy},
			},
			Phases:     []workflow.Phase{{ID: "phase", Kind: "implementation", Label: "phase", Validation: validation}},
			Flow:       []workflow.FlowStep{{Complete: "default"}},
			Completion: map[string]workflow.Completion{"default": {FinalValidation: validation}},
		},
	}
}

func completionRegressionCommand(counter string) string {
	return fmt.Sprintf("printf x >> %q; test -f completion.txt", counter)
}

func newCompletionRegressionEngine(t *testing.T, w *workflow.Workflow, p *completionRegressionProvider) *Engine {
	t.Helper()
	e, err := New(w, map[string]provider.Provider{"test": p}, Options{RepoRoot: w.Spec.Workspace.Root})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	return e
}

func seedCompletionFile(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "completion.txt"), []byte("seeded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "completion.txt")
	gitIn(t, repo, "commit", "-qm", "seed completion")
}

func assertCompletionRegressionMarker(t *testing.T, e *Engine, want bool) {
	t.Helper()
	ok, _, err := e.validCommitMarker(e.workflowCompleteMarker())
	if err != nil || ok != want {
		t.Fatalf("completion marker = %t err=%v, want %t", ok, err, want)
	}
}

func assertCompletionRegressionRepairState(t *testing.T, e *Engine, completion, validation string, attempts int, want bool) {
	t.Helper()
	record := e.standaloneRepairRecordForScope(completionValidationScope(completion, validation))
	var state standaloneRepairState
	ok, err := e.Store.GetJSON(record, &state)
	if err != nil || ok != want || (want && state.Attempts != attempts) {
		t.Fatalf("completion repair state = %+v ok=%t err=%v, want attempts=%d present=%t", state, ok, err, attempts, want)
	}
}

func assertStandaloneRepairState(t *testing.T, e *Engine, validation string, attempts int, want bool) {
	t.Helper()
	var state standaloneRepairState
	ok, err := e.Store.GetJSON(e.standaloneRepairRecordForScope(validation), &state)
	if err != nil || ok != want || (want && state.Attempts != attempts) {
		t.Fatalf("standalone repair state = %+v ok=%t err=%v, want attempts=%d present=%t", state, ok, err, attempts, want)
	}
}

func mustPhase(t *testing.T, e *Engine, id string) *workflow.Phase {
	t.Helper()
	p, err := e.phaseByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
