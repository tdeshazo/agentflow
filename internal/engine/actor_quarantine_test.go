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

func TestActorQuarantineImportsCompliantChangesAndCleansUp(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "actor-quarantine-compliant")
	var quarantinePath string
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		quarantinePath = request.Workspace
		if request.Workspace == repo {
			t.Fatal("actor received authoritative workspace")
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(repo, "work.txt"))
	if err != nil || string(content) != "complete\n" {
		t.Fatalf("imported actor change = %q err=%v", content, err)
	}
	if quarantinePath == "" {
		t.Fatal("provider did not receive a quarantine workspace")
	}
	if _, err := os.Stat(quarantinePath); !os.IsNotExist(err) {
		t.Fatalf("compliant actor quarantine remains: %v", err)
	}
}

func TestActorQuarantinePreservesRejectedChangesWithoutPoisoningPrimary(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "actor-quarantine-rejected")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.Workspace, "not-allowed.txt"), []byte("unsafe\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || safetyErr.quarantine == "" {
		t.Fatalf("run error = %v, want quarantined safety violation", err)
	}
	if !strings.Contains(err.Error(), safetyErr.quarantine) {
		t.Fatalf("safety error does not identify quarantine %q: %v", safetyErr.quarantine, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "work.txt")); !os.IsNotExist(err) {
		t.Fatalf("allowed portion of rejected actor delta poisoned primary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "not-allowed.txt")); !os.IsNotExist(err) {
		t.Fatalf("prohibited actor file poisoned primary: %v", err)
	}
	if got := string(mustReadFile(t, filepath.Join(safetyErr.quarantine, "work.txt"))); got != "complete\n" {
		t.Fatalf("quarantined compliant file = %q", got)
	}
	if got := string(mustReadFile(t, filepath.Join(safetyErr.quarantine, "not-allowed.txt"))); got != "unsafe\n" {
		t.Fatalf("quarantined prohibited file = %q", got)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); got != "" {
		t.Fatalf("authoritative workspace is dirty after rejection: %q", got)
	}

	var active ActivePhase
	if ok, readErr := e.Store.GetJSON(e.activeRecord(), &active); readErr != nil || !ok || active.QuarantinePath != safetyErr.quarantine {
		t.Fatalf("durable quarantine = %q ok=%t err=%v", active.QuarantinePath, ok, readErr)
	}
	snapshot, snapshotErr := e.statusSnapshot()
	if snapshotErr != nil || snapshot.QuarantinePath != safetyErr.quarantine {
		t.Fatalf("status quarantine = %q err=%v", snapshot.QuarantinePath, snapshotErr)
	}
	discovery, found, discoveryErr := e.Repo.FindDescriptor(w.Metadata.Name)
	if discoveryErr != nil || !found || discovery.Descriptor == nil {
		t.Fatalf("discover quarantined workflow: found=%t item=%+v err=%v", found, discovery, discoveryErr)
	}
	projection, projectionErr := discovery.Descriptor.ProjectStatus(e.Repo, discovery.Namespace)
	if projectionErr != nil || projection.QuarantinePath != safetyErr.quarantine {
		t.Fatalf("repository status quarantine = %q err=%v", projection.QuarantinePath, projectionErr)
	}
}

func TestActorQuarantineKeepsRejectedBaselinePinnedForInspection(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".baseline-ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-qm", "ignore baseline fixture")
	if err := os.WriteFile(filepath.Join(repo, ".baseline-ignored"), []byte("unique ignored baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := durableWorkflow(repo, "actor-quarantine-rejected-baseline")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("actor result\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.Workspace, "not-allowed.txt"), []byte("unsafe\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || safetyErr.quarantine == "" {
		t.Fatalf("run error = %v, want quarantined safety violation", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "work.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected actor poisoned primary workspace: %v", err)
	}
	var outcome ActorInvocationOutcome
	if ok, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); err != nil || !ok || outcome.BaselineTree == "" || outcome.Imported {
		t.Fatalf("rejected invocation outcome = %+v present=%t err=%v", outcome, ok, err)
	}
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("rejected pending invocation: present=%t err=%v", ok, err)
	}

	gitIn(t, repo, "gc", "--prune=now")
	if !e.Repo.ObjectExists(outcome.BaselineTree + "^{tree}") {
		t.Fatal("garbage collection pruned the rejected quarantine baseline")
	}
	if diff := gitIn(t, safetyErr.quarantine, "diff", outcome.BaselineTree, "--", "."); !strings.Contains(diff, "actor result") {
		t.Fatalf("rejected quarantine is not inspectable against its baseline: %q", diff)
	}

	restarted := newDurableEngine(t, w, p)
	if err := restarted.Run(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("restart error = %v, want durable safety failure", err)
	}
	if p.calls != 1 {
		t.Fatalf("durable safety restart invoked provider %d times", p.calls)
	}
	gitIn(t, repo, "gc", "--prune=now")
	if !restarted.Repo.ObjectExists(outcome.BaselineTree + "^{tree}") {
		t.Fatal("durable safety restart released the rejected quarantine baseline")
	}
}

func TestActorQuarantineRejectsOutOfScopePermissionOnlyChanges(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "actor-quarantine-permission-rejected")
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.Chmod(filepath.Join(request.Workspace, "README.md"), 0o600)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "out-of-scope file changed: README.md") {
		t.Fatalf("run error = %v, want permission-only scope violation", err)
	}
	info, statErr := os.Stat(filepath.Join(repo, "README.md"))
	if statErr != nil {
		t.Fatal(statErr)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("rejected permission change poisoned primary mode = %04o, want 0644", got)
	}
}
