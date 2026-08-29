package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestActorQuarantineAllowedSubmoduleChangeCompletesPhaseLifecycle(t *testing.T) {
	submodule := newDurableRepo(t)
	repoRoot := newDurableRepo(t)
	gitIn(
		t,
		repoRoot,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		submodule,
		"dependencies/child",
	)
	gitIn(t, repoRoot, "commit", "-qam", "add child submodule")

	w := durableWorkflow(repoRoot, "actor-submodule-phase-lifecycle")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"dependencies/child/allowed.txt"}
	w.Spec.Tools["gate"] = workflow.Tool{
		Type:    "shell",
		Command: "grep -qx complete dependencies/child/allowed.txt",
	}
	validation := w.Spec.Validation["phaseGate"]
	validation.Dependencies = []string{"dependencies/child/allowed.txt"}
	w.Spec.Validation["phaseGate"] = validation
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		child := filepath.Join(request.Workspace, "dependencies/child")
		if err := os.WriteFile(filepath.Join(child, "allowed.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, child, "add", "allowed.txt")
		gitIn(t, child, "commit", "-qm", "actor child change")
		return nil
	}}
	e := newDurableEngine(t, w, p)

	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("run full phase lifecycle: %v", err)
	}
	assertDurableCompletion(t, e, repoRoot)
	if got := string(mustReadFile(t, filepath.Join(repoRoot, "dependencies/child/allowed.txt"))); got != "complete\n" {
		t.Fatalf("imported child content = %q", got)
	}
}

func TestActorQuarantineCumulativeScopeIncludesPreexistingSubmoduleChanges(t *testing.T) {
	submodule := newDurableRepo(t)
	repoRoot := newDurableRepo(t)
	gitIn(
		t,
		repoRoot,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		submodule,
		"dependencies/child",
	)
	gitIn(t, repoRoot, "commit", "-qam", "add child submodule")

	w := durableWorkflow(repoRoot, "actor-submodule-cumulative-scope")
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"dependencies/child/allowed.txt"}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	primaryChild := filepath.Join(repoRoot, "dependencies/child")
	if err := os.WriteFile(filepath.Join(primaryChild, "outside.txt"), []byte("preexisting\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	quarantine, err := e.Repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	quarantineChild := filepath.Join(quarantine.Repo.Root, "dependencies/child")
	if err := os.WriteFile(filepath.Join(quarantineChild, "allowed.txt"), []byte("actor\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pending := PendingActorInvocation{
		Actor:               "worker",
		StartCommit:         quarantine.StartCommit,
		QuarantinePath:      quarantine.Repo.Root,
		BaselineTree:        quarantine.BaselineTree,
		BaselinePermissions: quarantine.BaselinePermissions,
		Submodules:          quarantine.Submodules,
	}

	result, err := e.reconcileActorQuarantine(pending, workflow.Agent{MayCommit: true})
	var violation *safetyViolation
	if !errors.As(err, &violation) || result.imported || !strings.Contains(err.Error(), "out-of-scope file changed: dependencies/child/outside.txt") {
		t.Fatalf("cumulative submodule scope: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(primaryChild, "allowed.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejected allowed child change reached authoritative worktree: %v", err)
	}
}

func TestActorQuarantineAuthorizesAndImportsAllowedNestedSubmoduleCommits(t *testing.T) {
	submodule := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(submodule, "child.txt"), []byte("child baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, submodule, "add", "child.txt")
	gitIn(t, submodule, "commit", "-qm", "child baseline")

	repoRoot := newDurableRepo(t)
	gitIn(
		t,
		repoRoot,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		submodule,
		"dependencies/child",
	)
	gitIn(t, repoRoot, "commit", "-qam", "add child submodule")

	repo := gitstate.Repo{Root: repoRoot}
	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	quarantineChild := filepath.Join(quarantine.Repo.Root, "dependencies/child")
	if err := os.WriteFile(filepath.Join(quarantineChild, "child.txt"), []byte("actor committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, quarantineChild, "add", "child.txt")
	gitIn(t, quarantineChild, "commit", "-qm", "actor child commit")
	actorChildHead := strings.TrimSpace(gitIn(t, quarantineChild, "rev-parse", "HEAD"))
	primaryChild := filepath.Join(repoRoot, "dependencies/child")
	if err := exec.Command("git", "-C", primaryChild, "cat-file", "-e", actorChildHead+"^{commit}").Run(); err == nil {
		t.Fatal("actor child commit unexpectedly exists in the authoritative submodule before import")
	}

	pending := PendingActorInvocation{
		Actor:               "worker",
		StartCommit:         quarantine.StartCommit,
		QuarantinePath:      quarantine.Repo.Root,
		BaselineTree:        quarantine.BaselineTree,
		BaselinePermissions: quarantine.BaselinePermissions,
		Submodules:          quarantine.Submodules,
	}
	e := &Engine{
		Repo:  repo,
		Store: gitstate.NewStore(repo, "actor-submodule-allowlist"),
		Workflow: &workflow.Workflow{Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{
				MutationPolicy: workflow.MutationPolicy{Allowed: []string{"dependencies/child/**"}},
			},
		}},
	}
	if err := e.Store.SetCommit(e.baseRecord(), quarantine.StartCommit); err != nil {
		t.Fatal(err)
	}

	result, err := e.reconcileActorQuarantine(pending, workflow.Agent{})
	var violation *safetyViolation
	if !errors.As(err, &violation) || result.authorized || !result.moved || result.imported {
		t.Fatalf("unauthorized child commit: result=%+v err=%v", result, err)
	}
	if got := strings.TrimSpace(gitIn(t, primaryChild, "rev-parse", "HEAD")); got == actorChildHead {
		t.Fatal("unauthorized child commit reached authoritative submodule")
	}

	result, err = e.reconcileActorQuarantine(pending, workflow.Agent{MayCommit: true})
	if err != nil || !result.authorized || !result.moved || !result.imported {
		t.Fatalf("authorized child commit: result=%+v err=%v", result, err)
	}
	if got := strings.TrimSpace(gitIn(t, primaryChild, "rev-parse", "HEAD")); got != actorChildHead {
		t.Fatalf("authoritative child HEAD = %q, want %q", got, actorChildHead)
	}
	if got := string(mustReadFile(t, filepath.Join(primaryChild, "child.txt"))); got != "actor committed\n" {
		t.Fatalf("authoritative child content = %q", got)
	}

	if err := os.WriteFile(filepath.Join(quarantine.Repo.Root, "outside.txt"), []byte("out of scope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = e.reconcileActorQuarantine(pending, workflow.Agent{MayCommit: true})
	if !errors.As(err, &violation) || result.imported || !strings.Contains(err.Error(), "out-of-scope file changed: outside.txt") {
		t.Fatalf("out-of-scope superproject change: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "outside.txt")); !os.IsNotExist(err) {
		t.Fatalf("out-of-scope file reached authoritative worktree: %v", err)
	}

	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
	gitIn(t, primaryChild, "cat-file", "-e", actorChildHead+"^{commit}")
}
