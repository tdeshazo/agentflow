package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/provider"
)

func TestResetCleansPendingActorQuarantine(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-pending-actor-quarantine")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
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

	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	assertResetRemovedActorQuarantine(t, e, quarantine.Repo.Root)
}

func TestResetCleansTerminalSafetyActorQuarantine(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".actor-baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-qm", "ignore actor baseline fixture")
	if err := os.WriteFile(filepath.Join(repo, ".actor-baseline"), []byte("unique ignored baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := durableWorkflow(repo, "reset-terminal-actor-quarantine")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(
			filepath.Join(request.Workspace, "not-allowed.txt"),
			[]byte("unsafe\n"),
			0o644,
		)
	}}
	e := newDurableEngine(t, w, p)

	runErr := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(runErr, &safetyErr) || safetyErr.quarantine == "" {
		t.Fatalf("run error = %v, want retained actor quarantine", runErr)
	}
	var outcome ActorInvocationOutcome
	if ok, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); err != nil || !ok {
		t.Fatalf("invocation outcome: present=%t err=%v", ok, err)
	}

	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	assertResetRemovedActorQuarantine(t, e, safetyErr.quarantine)
	gitIn(t, repo, "gc", "--prune=now")
	if e.Repo.ObjectExists(outcome.BaselineTree + "^{tree}") {
		t.Fatal("reset left the rejected actor baseline pinned")
	}
}

func TestResetCleansStandaloneSafetyActorQuarantine(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-standalone-actor-quarantine")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	pending := PendingActorInvocation{
		Version:             pendingActorInvocationVersion,
		Actor:               "worker",
		StartCommit:         quarantine.StartCommit,
		Role:                "validation-repair",
		ValidationScope:     "completion/default/final",
		QuarantinePath:      quarantine.Repo.Root,
		BaselineTree:        quarantine.BaselineTree,
		BaselinePermissions: quarantine.BaselinePermissions,
		Submodules:          quarantine.Submodules,
	}
	if err := e.Store.SetJSON(e.invocationOutcomeRecord(), ActorInvocationOutcome{
		PendingActorInvocation: pending,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(
		e.standaloneFailureRecordForScope(pending.ValidationScope),
		validationFailureEvidence{
			Validation:     pending.ValidationScope,
			FailureKind:    PhaseFailureSafety,
			QuarantinePath: quarantine.Repo.Root,
		},
	); err != nil {
		t.Fatal(err)
	}

	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	assertResetRemovedActorQuarantine(t, e, quarantine.Repo.Root)
}

func TestResetFinishesInterruptedActorQuarantineCleanup(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-interrupted-actor-cleanup")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
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
	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}

	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	assertResetRemovedActorQuarantine(t, e, quarantine.Repo.Root)
}

func TestResetPrunesActorQuarantineWhenWorktreeDirectoryDisappears(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-prunable-actor-cleanup")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
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
	if err := os.RemoveAll(quarantine.Repo.Root); err != nil {
		t.Fatal(err)
	}

	if err := e.Reset(); err != nil {
		t.Fatal(err)
	}
	assertResetRemovedActorQuarantine(t, e, quarantine.Repo.Root)
}

func TestResetRetainsStateWhenActorQuarantineCleanupIsUnsafe(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-unsafe-actor-quarantine")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	start, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	pending := PendingActorInvocation{
		Version:        pendingActorInvocationVersion,
		Actor:          "worker",
		StartCommit:    start,
		Role:           "phase",
		QuarantinePath: repo,
		BaselineTree:   strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD^{tree}")),
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		t.Fatal(err)
	}

	if err := e.Reset(); err == nil || !strings.Contains(err.Error(), "invalid actor quarantine path") {
		t.Fatalf("reset cleanup error = %v, want unsafe quarantine rejection", err)
	}
	if _, ok, err := e.Store.Resolve(e.baseRecord()); err != nil || !ok {
		t.Fatalf("base state after failed cleanup: present=%t err=%v", ok, err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || !ok {
		t.Fatalf("pending state after failed cleanup: present=%t err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("unsafe cleanup altered authoritative workspace: %v", err)
	}
}

func TestResetRetainsTerminalStateWithoutActorQuarantineAuthority(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "reset-missing-actor-cleanup-authority")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	phase, err := e.phaseByID("change")
	if err != nil {
		t.Fatal(err)
	}
	active, err := e.newActivePhaseFor(phase)
	if err != nil {
		t.Fatal(err)
	}
	active.FailureKind = PhaseFailureSafety
	active.QuarantinePath = quarantine.Repo.Root
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		t.Fatal(err)
	}

	if err := e.Reset(); err == nil || !strings.Contains(err.Error(), "no invocation cleanup authority") {
		t.Fatalf("reset cleanup error = %v, want missing authority rejection", err)
	}
	if _, ok, err := e.Store.Resolve(e.activeRecord()); err != nil || !ok {
		t.Fatalf("active state after failed cleanup: present=%t err=%v", ok, err)
	}
	if _, err := os.Stat(quarantine.Repo.Root); err != nil {
		t.Fatalf("quarantine after failed cleanup: %v", err)
	}
}

func assertResetRemovedActorQuarantine(t *testing.T, e *Engine, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("actor quarantine remains after reset: %v", err)
	}
	if worktrees := gitIn(t, e.Repo.Root, "worktree", "list", "--porcelain"); strings.Contains(worktrees, path) {
		t.Fatalf("actor quarantine remains registered after reset: %s", worktrees)
	}
	if _, ok, err := e.Store.Resolve(e.baseRecord()); err != nil || ok {
		t.Fatalf("workflow state after reset: present=%t err=%v", ok, err)
	}
}
