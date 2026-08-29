package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestActorWorktreeSnapshotsAndImportsWorkspaceDelta(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, ".gitignore", "ignored.txt\n")
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "committed\n")
	actorWorktreeGit(t, repo.Root, "add", ".gitignore", "tracked.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "workspace baseline")

	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "primary dirty\n")
	writeActorWorktreeFile(t, repo.Root, "untracked.txt", "primary untracked\n")
	writeActorWorktreeFile(t, repo.Root, "ignored.txt", "primary ignored\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath := quarantine.Repo.Root
	defer quarantine.Remove()
	for _, test := range []struct {
		name string
		want string
	}{
		{name: "tracked.txt", want: "primary dirty\n"},
		{name: "untracked.txt", want: "primary untracked\n"},
		{name: "ignored.txt", want: "primary ignored\n"},
	} {
		if got := readActorWorktreeFile(t, quarantine.Repo.Root, test.name); got != test.want {
			t.Fatalf("quarantine %s = %q, want %q", test.name, got, test.want)
		}
	}

	writeActorWorktreeFile(t, quarantine.Repo.Root, "tracked.txt", "actor change\n")
	writeActorWorktreeFile(t, quarantine.Repo.Root, "created.txt", "created by actor\n")
	if err := os.Remove(filepath.Join(quarantine.Repo.Root, "untracked.txt")); err != nil {
		t.Fatal(err)
	}
	finalTree, err := quarantine.FinalTree()
	if err != nil {
		t.Fatal(err)
	}
	finalPermissions, err := quarantine.FilePermissions()
	if err != nil {
		t.Fatal(err)
	}
	patch, err := quarantine.Patch(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || !applied {
		t.Fatalf("first actor import: applied=%t err=%v", applied, err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || applied {
		t.Fatalf("idempotent actor import: applied=%t err=%v", applied, err)
	}
	if got := readActorWorktreeFile(t, repo.Root, "tracked.txt"); got != "actor change\n" {
		t.Fatalf("imported tracked file = %q", got)
	}
	if got := readActorWorktreeFile(t, repo.Root, "created.txt"); got != "created by actor\n" {
		t.Fatalf("imported new file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted actor path remains: %v", err)
	}

	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantinePath); !os.IsNotExist(err) {
		t.Fatalf("compliant quarantine remains after cleanup: %v", err)
	}
}

func TestActorWorktreeOmitsIgnoredRuntimeControlFiles(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, ".gitignore", ".agentflow/\n.agents/skills/agentflow-spec/\n")
	actorWorktreeGit(t, repo.Root, "add", ".gitignore")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "ignore runtime controls")
	if err := os.MkdirAll(filepath.Join(repo.Root, ".agentflow", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo.Root, ".agents", "skills", "agentflow-spec"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeActorWorktreeFile(t, repo.Root, ".agentflow/workflows/task.yaml", "private workflow\n")
	writeActorWorktreeFile(t, repo.Root, ".agents/skills/agentflow-spec/SKILL.md", "private skill\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })

	for _, path := range []string{
		".agentflow/workflows/task.yaml",
		".agents/skills/agentflow-spec/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(quarantine.Repo.Root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("actor-private path %q is visible: %v", path, err)
		}
		if _, ok := quarantine.BaselinePermissions[path]; ok {
			t.Fatalf("actor-private path %q is present in baseline permissions", path)
		}
	}
}

func TestActorWorktreeRejectsTrackedRuntimeControlFiles(t *testing.T) {
	for _, path := range []string{
		".agentflow/workflows/task.yaml",
		".agents/skills/agentflow-spec/SKILL.md",
	} {
		t.Run(path, func(t *testing.T) {
			repo := newDiscoveryRepo(t)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(repo.Root, filepath.FromSlash(path))), 0o755); err != nil {
				t.Fatal(err)
			}
			writeActorWorktreeFile(t, repo.Root, path, "tracked runtime control\n")
			actorWorktreeGit(t, repo.Root, "add", path)
			actorWorktreeGit(t, repo.Root, "commit", "-qm", "track runtime control")

			_, err := repo.CreateActorWorktree()
			if err == nil || !strings.Contains(err.Error(), "would remain readable through repository history") {
				t.Fatalf("CreateActorWorktree() error = %v, want tracked private-path rejection", err)
			}
		})
	}
}

func TestActorWorktreeRecoverySurvivesTemporaryDirectoryChange(t *testing.T) {
	repo := newDiscoveryRepo(t)
	firstTemp := t.TempDir()
	secondTemp := t.TempDir()
	t.Setenv("TMPDIR", firstTemp)

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	writeActorWorktreeFile(t, quarantine.Repo.Root, "actor.txt", "recoverable\n")

	t.Setenv("TMPDIR", secondTemp)
	recovered, err := RecoverActorWorktree(
		repo,
		quarantine.Repo.Root,
		quarantine.StartCommit,
		quarantine.BaselineTree,
		quarantine.BaselinePermissions,
		quarantine.Submodules,
	)
	if err != nil {
		t.Fatalf("recover after TMPDIR change: %v", err)
	}
	if got := readActorWorktreeFile(t, recovered.Repo.Root, "actor.txt"); got != "recoverable\n" {
		t.Fatalf("recovered actor file = %q", got)
	}
	parent := recovered.Parent
	if err := recovered.Remove(); err != nil {
		t.Fatalf("remove recovered quarantine: %v", err)
	}
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("recovered quarantine parent remains after cleanup: %v", err)
	}
}

func TestActorWorktreeRecoverySurvivesImmediateGarbageCollection(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "committed\n")
	actorWorktreeGit(t, repo.Root, "add", "tracked.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "baseline")
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "dirty baseline\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	cleanupNeeded := true
	t.Cleanup(func() {
		if cleanupNeeded {
			_ = quarantine.Remove()
		}
	})
	baselineRef, err := actorBaselineRef(quarantine.Repo.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	writeActorWorktreeFile(t, quarantine.Repo.Root, "tracked.txt", "actor result\n")

	actorWorktreeGit(t, repo.Root, "gc", "--prune=now")
	recovered, err := RecoverActorWorktree(
		repo,
		quarantine.Repo.Root,
		quarantine.StartCommit,
		quarantine.BaselineTree,
		quarantine.BaselinePermissions,
		quarantine.Submodules,
	)
	if err != nil {
		t.Fatalf("recover after immediate garbage collection: %v", err)
	}
	finalTree, err := recovered.FinalTree()
	if err != nil {
		t.Fatal(err)
	}
	patch, err := recovered.Patch(finalTree)
	if err != nil {
		t.Fatalf("build recovered patch after immediate garbage collection: %v", err)
	}
	if len(patch) == 0 {
		t.Fatal("recovered actor patch is empty")
	}
	if err := recovered.Remove(); err != nil {
		t.Fatal(err)
	}
	cleanupNeeded = false
	if _, ok, err := readActorBaselinePin(repo, baselineRef); err != nil || ok {
		t.Fatalf("baseline pin after quarantine cleanup: present=%t err=%v", ok, err)
	}
}

func TestActorWorktreeCleanupFinishesAfterWorktreeRemoval(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "committed\n")
	actorWorktreeGit(t, repo.Root, "add", "tracked.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "baseline")
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "dirty baseline\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	baselineRef, err := actorBaselineRef(quarantine.Repo.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.run(nil, "worktree", "remove", "--force", quarantine.Repo.Root); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := readActorBaselinePin(repo, baselineRef); err != nil || !ok || got != quarantine.BaselineTree {
		t.Fatalf("baseline pin after worktree removal = %q present=%t err=%v, want %q", got, ok, err, quarantine.BaselineTree)
	}
	if err := CleanupRemovedActorWorktree(repo, quarantine.Repo.Root, quarantine.BaselineTree); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := readActorBaselinePin(repo, baselineRef); err != nil || ok {
		t.Fatalf("baseline pin after retry cleanup: present=%t err=%v", ok, err)
	}
	if _, err := os.Stat(quarantine.Parent); !os.IsNotExist(err) {
		t.Fatalf("quarantine parent after retry cleanup: %v", err)
	}
}

func TestActorWorktreeUsesCommonGitDirectoryFromLinkedWorktree(t *testing.T) {
	primary := newDiscoveryRepo(t)
	linkedPath := filepath.Join(t.TempDir(), "linked")
	actorWorktreeGit(t, primary.Root, "worktree", "add", "-q", "--detach", linkedPath, "HEAD")
	t.Cleanup(func() {
		_, _ = primary.run(nil, "worktree", "remove", "--force", linkedPath)
	})

	linked := Repo{Root: linkedPath}
	primaryRoot, err := actorQuarantineRoot(primary)
	if err != nil {
		t.Fatal(err)
	}
	linkedRoot, err := actorQuarantineRoot(linked)
	if err != nil {
		t.Fatal(err)
	}
	if linkedRoot != primaryRoot {
		t.Fatalf("linked-worktree quarantine root = %q, want common root %q", linkedRoot, primaryRoot)
	}
	for _, workspace := range []string{primary.Root, linked.Root} {
		relative, err := filepath.Rel(workspace, primaryRoot)
		if err != nil {
			t.Fatal(err)
		}
		if relative == "." || filepath.IsLocal(relative) {
			t.Fatalf("quarantine root %q is inside authoritative workspace %q", primaryRoot, workspace)
		}
	}

	quarantine, err := linked.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Dir(quarantine.Parent); got != primaryRoot {
		t.Fatalf("quarantine root = %q, want %q", got, primaryRoot)
	}
	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateActorWorktreeFallsBackWhenSiblingRootIsUnavailable(t *testing.T) {
	repo := newDiscoveryRepo(t)
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	preferredRoot, err := actorQuarantineRoot(repo)
	if err != nil {
		t.Fatal(err)
	}

	quarantine, err := repo.createActorWorktree(func(repo Repo) (string, error) {
		return ensureActorQuarantineRootWith(repo, func(root string) error {
			if root == preferredRoot {
				return actorQuarantineRootUnavailable(root, os.ErrPermission)
			}
			return initializeActorQuarantineRoot(root)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	fallbackRoot, err := actorQuarantineFallbackRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Dir(quarantine.Parent); got != fallbackRoot {
		t.Fatalf("quarantine root = %q, want fallback %q", got, fallbackRoot)
	}
	for _, path := range []string{filepath.Dir(fallbackRoot), fallbackRoot} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("quarantine directory %q permissions = %#o, want 0700", path, got)
		}
	}

	// A preferred root becoming available after a crash must not invalidate
	// recovery of a quarantine already durably recorded in the fallback.
	if _, err := ensureActorQuarantineRoot(repo); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverActorWorktree(
		repo,
		quarantine.Repo.Root,
		quarantine.StartCommit,
		quarantine.BaselineTree,
		quarantine.BaselinePermissions,
		quarantine.Submodules,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestActorWorktreeFallbackRecoverySurvivesCacheEnvironmentChange(t *testing.T) {
	tests := []struct {
		name       string
		setInitial func(*testing.T, string)
		setRestart func(*testing.T, string)
	}{
		{
			name: "XDG_CACHE_HOME",
			setInitial: func(t *testing.T, root string) {
				t.Setenv("XDG_CACHE_HOME", root)
			},
			setRestart: func(t *testing.T, root string) {
				t.Setenv("XDG_CACHE_HOME", root)
			},
		},
		{
			name: "HOME",
			setInitial: func(t *testing.T, root string) {
				t.Setenv("XDG_CACHE_HOME", "")
				t.Setenv("HOME", root)
			},
			setRestart: func(t *testing.T, root string) {
				t.Setenv("HOME", root)
			},
		},
		{
			name: "cache environment unavailable",
			setInitial: func(t *testing.T, root string) {
				t.Setenv("XDG_CACHE_HOME", root)
			},
			setRestart: func(t *testing.T, _ string) {
				t.Setenv("XDG_CACHE_HOME", "")
				t.Setenv("HOME", "")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDiscoveryRepo(t)
			test.setInitial(t, t.TempDir())
			preferredRoot, err := actorQuarantineRoot(repo)
			if err != nil {
				t.Fatal(err)
			}

			quarantine, err := repo.createActorWorktree(func(repo Repo) (string, error) {
				return ensureActorQuarantineRootWith(repo, func(root string) error {
					if root == preferredRoot {
						return actorQuarantineRootUnavailable(root, os.ErrPermission)
					}
					return initializeActorQuarantineRoot(root)
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			cleanupNeeded := true
			t.Cleanup(func() {
				if cleanupNeeded {
					_ = quarantine.Remove()
				}
			})
			writeActorWorktreeFile(t, quarantine.Repo.Root, "actor.txt", "recoverable\n")

			test.setRestart(t, t.TempDir())
			recovered, err := RecoverActorWorktree(
				repo,
				quarantine.Repo.Root,
				quarantine.StartCommit,
				quarantine.BaselineTree,
				quarantine.BaselinePermissions,
				quarantine.Submodules,
			)
			if err != nil {
				t.Fatalf("recover after cache environment change: %v", err)
			}
			if got := readActorWorktreeFile(t, recovered.Repo.Root, "actor.txt"); got != "recoverable\n" {
				t.Fatalf("recovered actor file = %q", got)
			}
			parent := recovered.Parent
			if err := recovered.Remove(); err != nil {
				t.Fatalf("remove recovered quarantine: %v", err)
			}
			cleanupNeeded = false
			if _, err := os.Stat(parent); !os.IsNotExist(err) {
				t.Fatalf("recovered quarantine parent remains after cleanup: %v", err)
			}
		})
	}
}

func TestActorWorktreeAcceptsLegacyQuarantinePathForRecovery(t *testing.T) {
	repo := newDiscoveryRepo(t)
	mainWorktree, repositoryID, err := actorQuarantineIdentity(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacyRootParent := filepath.Join(filepath.Dir(mainWorktree), legacyActorQuarantineRootDirectory)
	root := filepath.Join(legacyRootParent, repositoryID)
	if err := initializeActorQuarantineRoot(root); err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp(root, legacyActorQuarantineDirectoryPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(legacyRootParent) })
	path := filepath.Join(parent, actorWorktreeDirectoryName)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := actorQuarantineParent(repo, path)
	if err != nil {
		t.Fatalf("accept legacy actor quarantine path: %v", err)
	}
	if got != parent {
		t.Fatalf("legacy actor quarantine parent = %q, want %q", got, parent)
	}
}

func TestCreateActorWorktreeRejectsFallbackInsideAuthoritativeWorkspace(t *testing.T) {
	repo := newDiscoveryRepo(t)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(repo.Root, ".cache"))
	preferredRoot, err := actorQuarantineRoot(repo)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.createActorWorktree(func(repo Repo) (string, error) {
		return ensureActorQuarantineRootWith(repo, func(root string) error {
			if root == preferredRoot {
				return actorQuarantineRootUnavailable(root, os.ErrPermission)
			}
			return initializeActorQuarantineRoot(root)
		})
	})
	if err == nil {
		t.Fatal("CreateActorWorktree accepted a fallback root inside the authoritative workspace")
	}
	if _, err := os.Stat(filepath.Join(repo.Root, ".cache")); !os.IsNotExist(err) {
		t.Fatalf("unsafe fallback cache was created: %v", err)
	}
}

func TestCreateActorWorktreeRejectsSymlinkedQuarantineRoot(t *testing.T) {
	repo := newDiscoveryRepo(t)
	root, err := actorQuarantineRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Dir(root)); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CreateActorWorktree(); err == nil {
		t.Fatal("CreateActorWorktree accepted a quarantine root symlinked outside the Git directory")
	}
	entries, err := os.ReadDir(external)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quarantine creation escaped through symlink: %v", entries)
	}
}

func TestCreateActorWorktreeRejectsUntrackedSymlinkOutsideQuarantine(t *testing.T) {
	repo := newDiscoveryRepo(t)
	authoritativePath := filepath.Join(repo.Root, "authoritative.txt")
	writeActorWorktreeFile(t, repo.Root, "authoritative.txt", "authoritative\n")
	actorWorktreeGit(t, repo.Root, "add", "authoritative.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "authoritative baseline")
	if err := os.Symlink(authoritativePath, filepath.Join(repo.Root, "escape")); err != nil {
		t.Fatal(err)
	}

	quarantine, err := repo.CreateActorWorktree()
	if err == nil {
		_ = quarantine.Remove()
		t.Fatal("CreateActorWorktree accepted an untracked symlink targeting the authoritative workspace")
	}
}

func TestValidateActorWorktreeSymlinks(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		wantErr bool
	}{
		{
			name: "relative target inside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeActorWorktreeFile(t, root, "target.txt", "target\n")
				if err := os.Symlink("target.txt", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "absolute target inside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeActorWorktreeFile(t, root, "target.txt", "target\n")
				if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "broken target inside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("missing/target.txt", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "relative broken target outside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("../missing/target.txt", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
		{
			name: "absolute target outside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
		{
			name: "chained target outside quarantine",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink(t.TempDir(), filepath.Join(root, "redirect")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("redirect/target.txt", filepath.Join(root, "link")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
		{
			name: "cycle",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("second", filepath.Join(root, "first")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("first", filepath.Join(root, "second")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.setup(t, root)
			err := validateActorWorktreeSymlinks(root)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateActorWorktreeSymlinks() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestActorWorktreeSnapshotsIgnoredFileChanges(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, ".gitignore", "*.ignored\n")
	actorWorktreeGit(t, repo.Root, "add", ".gitignore")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "ignore actor fixtures")

	writeActorWorktreeFile(t, repo.Root, "modified.ignored", "primary modified baseline\n")
	writeActorWorktreeFile(t, repo.Root, "deleted.ignored", "primary deleted baseline\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Remove()

	writeActorWorktreeFile(t, quarantine.Repo.Root, "modified.ignored", "actor modification\n")
	if err := os.Remove(filepath.Join(quarantine.Repo.Root, "deleted.ignored")); err != nil {
		t.Fatal(err)
	}
	writeActorWorktreeFile(t, quarantine.Repo.Root, "created.ignored", "actor creation\n")

	finalTree, err := quarantine.FinalTree()
	if err != nil {
		t.Fatal(err)
	}
	finalPermissions, err := quarantine.FilePermissions()
	if err != nil {
		t.Fatal(err)
	}
	changedPaths, err := quarantine.ChangedPaths(finalTree, finalPermissions)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"created.ignored", "deleted.ignored", "modified.ignored"}
	if !slices.Equal(changedPaths, wantPaths) {
		t.Fatalf("ignored changed paths = %q, want %q", changedPaths, wantPaths)
	}

	patch, err := quarantine.Patch(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || !applied {
		t.Fatalf("import ignored actor changes: applied=%t err=%v", applied, err)
	}
	if got := readActorWorktreeFile(t, repo.Root, "modified.ignored"); got != "actor modification\n" {
		t.Fatalf("imported modified ignored file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "deleted.ignored")); !os.IsNotExist(err) {
		t.Fatalf("deleted ignored actor path remains: %v", err)
	}
	if got := readActorWorktreeFile(t, repo.Root, "created.ignored"); got != "actor creation\n" {
		t.Fatalf("imported created ignored file = %q", got)
	}
}

func TestActorWorktreePreservesRestrictiveFilePermissions(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, "existing.txt", "existing\n")
	actorWorktreeGit(t, repo.Root, "add", "existing.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "permission baseline")
	if err := os.Chmod(filepath.Join(repo.Root, "existing.txt"), 0o640); err != nil {
		t.Fatal(err)
	}

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Remove()
	baselineInfo, err := os.Stat(filepath.Join(quarantine.Repo.Root, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := baselineInfo.Mode().Perm(); got != 0o640 {
		t.Fatalf("quarantine baseline mode = %04o, want 0640", got)
	}

	if err := os.Chmod(filepath.Join(quarantine.Repo.Root, "existing.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(quarantine.Repo.Root, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	finalTree, err := quarantine.FinalTree()
	if err != nil {
		t.Fatal(err)
	}
	finalPermissions, err := quarantine.FilePermissions()
	if err != nil {
		t.Fatal(err)
	}
	patch, err := quarantine.Patch(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || !applied {
		t.Fatalf("import permission changes: applied=%t err=%v", applied, err)
	}
	if err := os.Chmod(filepath.Join(repo.Root, "secret.txt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || applied {
		t.Fatalf("idempotent permission import: applied=%t err=%v", applied, err)
	}

	for _, name := range []string{"existing.txt", "secret.txt"} {
		info, err := os.Stat(filepath.Join(repo.Root, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("imported %s mode = %04o, want 0600", name, got)
		}
	}
}

func TestActorWorktreeReportsPermissionOnlyChanges(t *testing.T) {
	repo := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, repo.Root, "tracked.txt", "unchanged\n")
	actorWorktreeGit(t, repo.Root, "add", "tracked.txt")
	actorWorktreeGit(t, repo.Root, "commit", "-qm", "permission baseline")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Remove()
	if err := os.Chmod(filepath.Join(quarantine.Repo.Root, "tracked.txt"), 0o600); err != nil {
		t.Fatal(err)
	}

	finalTree, err := quarantine.FinalTree()
	if err != nil {
		t.Fatal(err)
	}
	finalPermissions, err := quarantine.FilePermissions()
	if err != nil {
		t.Fatal(err)
	}
	changedPaths, err := quarantine.ChangedPaths(finalTree, finalPermissions)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tracked.txt"}; !slices.Equal(changedPaths, want) {
		t.Fatalf("permission-only changed paths = %q, want %q", changedPaths, want)
	}
	patch, err := quarantine.Patch(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil || applied {
		t.Fatalf("import permission-only change: applied=%t err=%v", applied, err)
	}
	info, err := os.Stat(filepath.Join(repo.Root, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("imported permission-only mode = %04o, want 0600", got)
	}
}

func TestActorWorktreeRejectsSpecialPermissionBits(t *testing.T) {
	repo := newDiscoveryRepo(t)
	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Remove()

	path := filepath.Join(quarantine.Repo.Root, "setuid.txt")
	if err := os.WriteFile(path, []byte("unsafe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if _, err := quarantine.FilePermissions(); err == nil {
		t.Fatal("FilePermissions accepted setuid permission metadata")
	}
}

func TestActorWorktreePreservesInitializedSubmodules(t *testing.T) {
	submodule := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, submodule.Root, "input.txt", "submodule input\n")
	actorWorktreeGit(t, submodule.Root, "add", "input.txt")
	actorWorktreeGit(t, submodule.Root, "commit", "-qm", "submodule input")

	repo := newDiscoveryRepo(t)
	actorWorktreeGit(
		t,
		repo.Root,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		submodule.Root,
		"dependencies/module",
	)
	actorWorktreeGit(t, repo.Root, "commit", "-qam", "add initialized submodule")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath := quarantine.Repo.Root
	defer quarantine.Remove()

	want := "submodule input\n"
	path := filepath.Join(quarantine.Repo.Root, "dependencies/module/input.txt")
	if got := readActorWorktreeFile(t, filepath.Dir(path), filepath.Base(path)); got != want {
		t.Fatalf("quarantine submodule input = %q, want %q", got, want)
	}

	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantinePath); !os.IsNotExist(err) {
		t.Fatalf("submodule quarantine remains after cleanup: %v", err)
	}
}

func TestActorWorktreeRemoveRejectsUnsafePath(t *testing.T) {
	primary := newDiscoveryRepo(t)
	parent := t.TempDir()
	path := filepath.Join(parent, actorWorktreeDirectoryName)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(parent, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := &ActorWorktree{
		Primary: primary,
		Repo:    Repo{Root: path},
		Parent:  parent,
	}
	if err := worktree.Remove(); err == nil {
		t.Fatal("Remove accepted a worktree outside a runtime quarantine directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("unsafe cleanup removed unrelated content: %v", err)
	}
}

func TestActorWorktreeRemoveRetainsParentWhenGitRemovalFails(t *testing.T) {
	primary := newDiscoveryRepo(t)
	root, err := ensureActorQuarantineRoot(primary)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp(root, actorQuarantineDirectoryPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	path := filepath.Join(parent, actorWorktreeDirectoryName)
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := &ActorWorktree{
		Primary: primary,
		Repo:    Repo{Root: path},
		Parent:  parent,
	}
	if err := worktree.Remove(); err == nil {
		t.Fatal("Remove succeeded for an unregistered worktree")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("failed Git removal recursively deleted quarantine parent: %v", err)
	}
}

func actorWorktreeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeActorWorktreeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readActorWorktreeFile(t *testing.T, dir, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
