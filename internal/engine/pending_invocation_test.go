package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		Version: legacyPendingActorInvocationVersion, Actor: "repair", StartCommit: active.StartCommit,
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
		Version: legacyPendingActorInvocationVersion, Actor: "worker", StartCommit: active.StartCommit,
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

func TestReconcilePendingInvocationV1UsesLegacyPrimaryWorkspaceSemantics(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-v1-primary-workspace")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	pending := PendingActorInvocation{
		Version:        legacyPendingActorInvocationVersion,
		Actor:          "worker",
		StartCommit:    start,
		Role:           "phase",
		QuarantinePath: repo,
		BaselineTree:   "ignored-by-version-1",
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		t.Fatal(err)
	}

	moved, err := e.reconcilePendingInvocation()
	if err != nil || moved {
		t.Fatalf("reconcile version 1 invocation: moved=%t err=%v", moved, err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("version 1 pending record: ok=%t err=%v", ok, err)
	}
	var outcome ActorInvocationOutcome
	if ok, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); err != nil || !ok {
		t.Fatalf("version 1 invocation outcome: ok=%t err=%v", ok, err)
	}
	if outcome.Version != legacyPendingActorInvocationVersion || outcome.Imported {
		t.Fatalf("version 1 invocation outcome = %+v", outcome)
	}
}

func TestReconcilePendingInvocationV2RequiresQuarantineAuthority(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-v2-requires-quarantine")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	pending := PendingActorInvocation{
		Version:     pendingActorInvocationVersion,
		Actor:       "worker",
		StartCommit: start,
		Role:        "phase",
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		t.Fatal(err)
	}

	if moved, err := e.reconcilePendingInvocation(); err == nil || moved || !strings.Contains(err.Error(), "pending actor quarantine is incomplete") {
		t.Fatalf("reconcile incomplete version 2 invocation: moved=%t err=%v", moved, err)
	}
	var persisted PendingActorInvocation
	if ok, err := e.Store.GetJSON(e.pendingInvocationRecord(), &persisted); err != nil || !ok || persisted.Version != pendingActorInvocationVersion {
		t.Fatalf("version 2 pending invocation after rejected recovery: %+v ok=%t err=%v", persisted, ok, err)
	}
}

func TestParallelRequiresChangeSurvivesIdempotentQuarantineReconciliation(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-idempotent-reconciliation", []string{"left", "right"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		path := filepath.Join(request.Workspace, "left", "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("left\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("left")
	if err != nil {
		t.Fatal(err)
	}
	nodeEngine := e.parallelNodeEngine("test-batch", phase.ID)
	active, err := nodeEngine.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	active.ParallelBatch = "test-batch"
	if err := nodeEngine.Store.SetJSON(nodeEngine.activeRecord(), active); err != nil {
		t.Fatal(err)
	}
	request := provider.Request{
		Workspace: repo,
		Metadata: map[string]string{
			"actor": "worker",
			"phase": phase.ID,
		},
	}
	if _, err := nodeEngine.invokeAgent(
		context.Background(),
		"worker",
		w.Spec.Agents["worker"],
		p,
		request,
		PendingActorInvocation{Role: "phase", PhaseID: phase.ID},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeEngine.reconcilePendingInvocation(); err != nil {
		t.Fatal(err)
	}

	var outcome ActorInvocationOutcome
	if ok, err := nodeEngine.Store.GetJSON(nodeEngine.invocationOutcomeRecord(), &outcome); err != nil || !ok {
		t.Fatalf("read imported outcome: ok=%t err=%v", ok, err)
	}
	if len(outcome.ChangedPaths) == 0 {
		t.Fatal("initial reconciliation did not record the actor's changed paths")
	}
	wantChangedPaths := append([]string(nil), outcome.ChangedPaths...)
	if _, err := os.Stat(outcome.QuarantinePath); !os.IsNotExist(err) {
		t.Fatalf("reconciled quarantine still exists: %v", err)
	}
	// Recreate the crash window after the imported outcome and quarantine
	// cleanup were durable but before the pending record was deleted.
	if err := nodeEngine.Store.SetJSON(nodeEngine.pendingInvocationRecord(), outcome.PendingActorInvocation); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeEngine.reconcilePendingInvocation(); err != nil {
		t.Fatal(err)
	}
	var recoveredOutcome ActorInvocationOutcome
	if ok, err := nodeEngine.Store.GetJSON(nodeEngine.invocationOutcomeRecord(), &recoveredOutcome); err != nil || !ok {
		t.Fatalf("read recovered outcome: ok=%t err=%v", ok, err)
	}
	if !slices.Equal(recoveredOutcome.ChangedPaths, wantChangedPaths) {
		t.Fatalf("recovered changed paths = %v, want %v", recoveredOutcome.ChangedPaths, wantChangedPaths)
	}
	if err := nodeEngine.assertNetChange(phase, &active); err != nil {
		t.Fatalf("parallel requiresChange rejected the durable actor delta after recovery: %v", err)
	}
}

func TestReconcilePendingActorQuarantineSurvivesImmediateGarbageCollection(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-quarantine-gc")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("primary dirty baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	if err := os.WriteFile(filepath.Join(quarantine.Repo.Root, "work.txt"), []byte("actor result\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pending := PendingActorInvocation{
		Version:             pendingActorInvocationVersion,
		Actor:               "worker",
		StartCommit:         quarantine.StartCommit,
		Role:                "phase",
		QuarantinePath:      quarantine.Repo.Root,
		BaselineTree:        quarantine.BaselineTree,
		BaselinePermissions: quarantine.BaselinePermissions,
		Submodules:          quarantine.Submodules,
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		t.Fatal(err)
	}

	gitIn(t, repo, "gc", "--prune=now")
	if !quarantine.Repo.ObjectExists(quarantine.BaselineTree + "^{tree}") {
		t.Fatal("garbage collection pruned the pending quarantine baseline")
	}
	if moved, err := e.reconcilePendingInvocation(); err != nil || moved {
		t.Fatalf("reconcile garbage-collected quarantine: moved=%t err=%v", moved, err)
	}
	if got := string(mustReadFile(t, filepath.Join(repo, "work.txt"))); got != "actor result\n" {
		t.Fatalf("recovered actor result = %q", got)
	}
	if _, err := os.Stat(quarantine.Repo.Root); !os.IsNotExist(err) {
		t.Fatalf("reconciled quarantine remains: %v", err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("pending invocation after reconciliation: present=%t err=%v", ok, err)
	}
	gitIn(t, repo, "gc", "--prune=now")
	if e.Repo.ObjectExists(quarantine.BaselineTree + "^{tree}") {
		t.Fatal("cleaned quarantine baseline remains pinned")
	}
}

func TestReconcilePendingInvocationRejectsUnsafeQuarantinePath(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-unsafe-quarantine")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	baselineTree := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD^{tree}"))
	pending := PendingActorInvocation{
		Version:        pendingActorInvocationVersion,
		Actor:          "worker",
		StartCommit:    start,
		Role:           "phase",
		QuarantinePath: repo,
		BaselineTree:   baselineTree,
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		t.Fatal(err)
	}

	if _, err := e.reconcilePendingInvocation(); err == nil || !strings.Contains(err.Error(), "invalid actor quarantine path") {
		t.Fatalf("reconcile unsafe quarantine error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("unsafe recovery cleanup removed the authoritative workspace: %v", err)
	}
	var persisted PendingActorInvocation
	if ok, err := e.Store.GetJSON(e.pendingInvocationRecord(), &persisted); err != nil || !ok || persisted.QuarantinePath != repo {
		t.Fatalf("pending invocation after rejected cleanup: %+v ok=%t err=%v", persisted, ok, err)
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
		Version: legacyPendingActorInvocationVersion, Actor: "worker", StartCommit: active.StartCommit,
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
	w := completionRepairWorkflow(repo, "pending-completion-repair")
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
		Version: legacyPendingActorInvocationVersion, Actor: "repair", StartCommit: start,
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
