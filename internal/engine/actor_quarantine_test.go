package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
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

func TestActorQuarantineIgnoresUnchangedIgnoredBaselineFiles(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".agentflow/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-qm", "ignore local agentflow files")
	workflowPath := filepath.Join(repo, ".agentflow", "workflows", "example.yaml")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflowPath, []byte("workflow baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := durableWorkflow(repo, "actor-quarantine-ignored-baseline")
	w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{
		ID:    "runtime-control",
		Paths: []string{".agentflow/workflows/**"},
		Mode:  "exact-hash",
	}}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if _, err := os.Stat(filepath.Join(request.Workspace, ".agentflow", "workflows", "example.yaml")); !os.IsNotExist(err) {
			return errors.New("actor can read the private workflow")
		}
		if len(request.FilesystemBoundary) != 1 ||
			request.FilesystemBoundary[0].Access != provider.FilesystemDeny {
			return errors.New("provider did not receive the authoritative read boundary")
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("run with unchanged ignored baseline: %v", err)
	}
	assertDurableCompletion(t, e, repo)
	if got := string(mustReadFile(t, workflowPath)); got != "workflow baseline\n" {
		t.Fatalf("ignored workflow baseline = %q", got)
	}
}

func TestActorQuarantineDetectsAuthoritativeRuntimeControlTampering(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".agentflow/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-qm", "ignore runtime controls")
	workflowPath := filepath.Join(repo, ".agentflow", "workflows", "example.yaml")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflowPath, []byte("workflow baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := durableWorkflow(repo, "actor-quarantine-authoritative-integrity")
	w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{
		ID:    "runtime-control",
		Paths: []string{".agentflow/workflows/**"},
		Mode:  "exact-hash",
	}}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(workflowPath, []byte("tampered\n"), 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "protected integrity rule runtime-control changed") {
		t.Fatalf("Run() error = %v, want authoritative runtime-control integrity violation", err)
	}
}

func TestActorQuarantineStillProtectsVisibleFilesInMixedIntegrityRule(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".agentflow/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "protected.txt"), []byte("protected baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore", "protected.txt")
	gitIn(t, repo, "commit", "-qm", "add protected baseline")
	workflowPath := filepath.Join(repo, ".agentflow", "workflows", "example.yaml")
	if err := os.MkdirAll(filepath.Dir(workflowPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflowPath, []byte("workflow baseline\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := durableWorkflow(repo, "actor-quarantine-mixed-integrity")
	w.Spec.Workspace.MutationPolicy.Allowed = append(w.Spec.Workspace.MutationPolicy.Allowed, "protected.txt")
	w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{
		ID:    "mixed-control-and-source",
		Paths: []string{"protected.txt", ".agentflow/workflows/**"},
		Mode:  "exact-hash",
	}}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "protected.txt"), []byte("actor change\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "protected integrity rule mixed-control-and-source changed") {
		t.Fatalf("Run() error = %v, want visible-file integrity violation", err)
	}
}

func TestActorQuarantineRejectsCreationOfRuntimePrivatePaths(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "actor-quarantine-private-path-creation")
	w.Spec.Workspace.MutationPolicy.Allowed = append(w.Spec.Workspace.MutationPolicy.Allowed, ".agentflow/**")
	w.Spec.Workspace.MutationPolicy.IgnoredControlFiles = []string{".agentflow/**"}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		path := filepath.Join(request.Workspace, ".agentflow", "workflows", "injected.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("actor-created control\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)

	err := e.Run(context.Background())
	var safetyErr *safetyViolation
	if !errors.As(err, &safetyErr) || !strings.Contains(err.Error(), "actor changed runtime-private path .agentflow/workflows/injected.yaml") {
		t.Fatalf("Run() error = %v, want immutable runtime-private path violation", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".agentflow", "workflows", "injected.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("actor-created runtime control reached authoritative workspace: %v", statErr)
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
	gitIn(t, safetyErr.quarantine, "cat-file", "-e", outcome.BaselineTree+"^{tree}")
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
	gitIn(t, safetyErr.quarantine, "cat-file", "-e", outcome.BaselineTree+"^{tree}")
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

func TestChangedPathPermissionsPreservesDisjointSiblingState(t *testing.T) {
	tests := []struct {
		name        string
		prepareLeft func(*testing.T, string)
		assertLeft  func(*testing.T, string)
	}{
		{
			name: "deleted file remains deleted",
			prepareLeft: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			assertLeft: func(t *testing.T, path string) {
				t.Helper()
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("accepted sibling deletion was reverted: %v", err)
				}
			},
		},
		{
			name: "chmod remains in effect",
			prepareLeft: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			assertLeft: func(t *testing.T, path string) {
				t.Helper()
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != 0o600 {
					t.Fatalf("accepted sibling mode = %04o, want 0600", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			left := filepath.Join(repo, "left.txt")
			right := filepath.Join(repo, "right.txt")
			if err := os.WriteFile(left, []byte("left\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(right, []byte("right\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			tt.prepareLeft(t, left)

			// The later actor observed left.txt at the baseline mode, but only
			// right.txt belongs to its actual changed-path delta.
			finalPermissions := gitstate.FilePermissions{
				"left.txt":  0o644,
				"right.txt": 0o600,
			}
			permissions := changedPathPermissions(finalPermissions, []string{"right.txt"})
			if _, ok := permissions["left.txt"]; ok {
				t.Fatal("permission delta retained a disjoint sibling path")
			}
			if _, err := (gitstate.Repo{Root: repo}).ApplyPatchIdempotent(nil, permissions); err != nil {
				t.Fatalf("apply later actor permission delta: %v", err)
			}
			tt.assertLeft(t, left)
			rightInfo, err := os.Stat(right)
			if err != nil {
				t.Fatal(err)
			}
			if got := rightInfo.Mode().Perm(); got != 0o600 {
				t.Fatalf("later actor mode = %04o, want 0600", got)
			}
		})
	}
}
