package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestReconcilePendingInvocationAttributesAuthorizedRepairCommit(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-authorized-repair")
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: true}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("repair\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "work.txt")
	gitIn(t, repo, "commit", "-qm", "repair commit")
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), PendingActorInvocation{
		Version: pendingActorInvocationVersion, Actor: "repair", StartCommit: active.StartCommit,
		Role: "validation-repair", PhaseID: phase.ID, ValidationScope: "phase/change/phaseGate",
	}); err != nil {
		t.Fatal(err)
	}

	moved, err := e.reconcilePendingInvocation()
	if err != nil || !moved {
		t.Fatalf("reconcile pending authorized repair: moved=%t err=%v", moved, err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("pending record: ok=%t err=%v", ok, err)
	}
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.CommitActor != "repair" {
		t.Fatalf("active attribution: %+v ok=%t err=%v", active, ok, err)
	}
	var outcome ActorInvocationOutcome
	if ok, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); err != nil || !ok || !outcome.HeadMoved || !outcome.Authorized || outcome.Actor != "repair" {
		t.Fatalf("invocation outcome: %+v ok=%t err=%v", outcome, ok, err)
	}
	if ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil || ok {
		t.Fatalf("reconciliation created acceptance evidence: ok=%t err=%v", ok, err)
	}
}

func TestRunPhaseActorRetainsDurableInvocationAttribution(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-primary-attribution")
	e := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "primary actor commit")
		return nil
	}})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}

	if err := e.runPhaseActor(context.Background(), phase, phase.Prompt, &active); err != nil {
		t.Fatal(err)
	}
	if !active.ActorCompleted || active.CommitActor != "worker" {
		t.Fatalf("in-memory active state = %+v", active)
	}
	var persisted ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &persisted); err != nil || !ok || !persisted.ActorCompleted || persisted.CommitActor != "worker" {
		t.Fatalf("persisted active state = %+v ok=%t err=%v", persisted, ok, err)
	}
}

func TestReconcilePendingInvocationWithoutHeadMovementDoesNotAttributeCommit(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-no-commit")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), PendingActorInvocation{
		Version: pendingActorInvocationVersion, Actor: "worker", StartCommit: active.StartCommit,
		Role: "phase", PhaseID: phase.ID,
	}); err != nil {
		t.Fatal(err)
	}

	moved, err := e.reconcilePendingInvocation()
	if err != nil || moved {
		t.Fatalf("reconcile pending no-commit invocation: moved=%t err=%v", moved, err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("pending record: ok=%t err=%v", ok, err)
	}
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.CommitActor != "" || active.ActorCompleted {
		t.Fatalf("active state after no-commit reconciliation: %+v ok=%t err=%v", active, ok, err)
	}
}

func TestReconcilePendingInvocationFailsClosedBeforeRecoveryWork(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-unauthorized-primary")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	providerImpl := &durableProvider{}
	e := newDurableEngine(t, w, providerImpl)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("unauthorized\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "work.txt")
	gitIn(t, repo, "commit", "-qm", "unauthorized actor commit")
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), PendingActorInvocation{
		Version: pendingActorInvocationVersion, Actor: "worker", StartCommit: active.StartCommit,
		Role: "phase", PhaseID: phase.ID,
	}); err != nil {
		t.Fatal(err)
	}

	_, err = e.reconcilePendingInvocation()
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || safetyErr.actor != "worker" {
		t.Fatalf("reconcile error = %v, want worker safety violation", err)
	}
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.FailureKind != PhaseFailureSafety || active.CommitActor != "worker" {
		t.Fatalf("active safety state: %+v ok=%t err=%v", active, ok, err)
	}
	if err := e.recoverActive(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("recovery error = %v, want persisted safety violation", err)
	}
	if providerImpl.calls != 0 {
		t.Fatalf("recovery ran provider %d times", providerImpl.calls)
	}
}

func TestReconcilePendingInvocationRetainsCompletionRepairAttribution(t *testing.T) {
	repo := newDurableRepo(t)
	w := v1Alpha2CompletionRepairWorkflow(repo, "pending-completion-repair")
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model", MayCommit: true}
	e := newSchedulingEngine(t, w, &schedulingProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "completion.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "completion.txt")
	gitIn(t, repo, "commit", "-qm", "completion repair commit")
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), PendingActorInvocation{
		Version: pendingActorInvocationVersion, Actor: "repair", StartCommit: start,
		Role: "validation-repair", ValidationScope: "completion/default/final",
	}); err != nil {
		t.Fatal(err)
	}

	moved, err := e.reconcilePendingInvocation()
	if err != nil || !moved {
		t.Fatalf("reconcile completion repair: moved=%t err=%v", moved, err)
	}
	var outcome ActorInvocationOutcome
	if ok, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); err != nil || !ok || outcome.Actor != "repair" || outcome.ValidationScope != "completion/default/final" || !outcome.Authorized {
		t.Fatalf("completion outcome: %+v ok=%t err=%v", outcome, ok, err)
	}
	if err := e.runCompletionValidation(context.Background(), "default", "final"); err != nil {
		t.Fatalf("completion validation after reconciliation: %v", err)
	}
}
