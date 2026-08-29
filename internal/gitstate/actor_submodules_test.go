package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestActorWorktreeImportsUncommittedSubmoduleChanges(t *testing.T) {
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
	primarySubmodule := filepath.Join(repo.Root, "dependencies/module")
	writeActorWorktreeFile(t, primarySubmodule, "input.txt", "primary committed input\n")
	actorWorktreeGit(t, primarySubmodule, "add", "input.txt")
	actorWorktreeGit(t, primarySubmodule, "commit", "-qm", "primary child commit")
	writeActorWorktreeFile(t, primarySubmodule, "input.txt", "primary dirty input\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })

	quarantineSubmodule := filepath.Join(quarantine.Repo.Root, "dependencies/module")
	if got := readActorWorktreeFile(t, quarantineSubmodule, "input.txt"); got != "primary dirty input\n" {
		t.Fatalf("quarantine submodule baseline = %q, want primary dirty input", got)
	}
	writeActorWorktreeFile(t, quarantineSubmodule, "input.txt", "actor change\n")

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
	if imported, err := quarantine.ImportSubmoduleChanges(); err != nil || !imported {
		t.Fatalf("import submodule changes: imported=%t err=%v", imported, err)
	}
	if _, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil {
		t.Fatal(err)
	}

	if got := readActorWorktreeFile(t, primarySubmodule, "input.txt"); got != "actor change\n" {
		t.Fatalf("imported submodule input = %q, want actor change", got)
	}
	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quarantine.Repo.Root); !os.IsNotExist(err) {
		t.Fatalf("submodule quarantine remains after cleanup: %v", err)
	}
}

func TestActorWorktreeImportsNestedCommittedAndUncommittedSubmoduleChanges(t *testing.T) {
	leaf := newDiscoveryRepo(t)
	writeActorWorktreeFile(t, leaf.Root, "committed.txt", "leaf committed baseline\n")
	writeActorWorktreeFile(t, leaf.Root, "dirty.txt", "leaf dirty baseline\n")
	actorWorktreeGit(t, leaf.Root, "add", ".")
	actorWorktreeGit(t, leaf.Root, "commit", "-qm", "leaf baseline")

	middle := newDiscoveryRepo(t)
	actorWorktreeGit(
		t,
		middle.Root,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		leaf.Root,
		"dependencies/leaf",
	)
	actorWorktreeGit(t, middle.Root, "commit", "-qam", "add leaf submodule")

	repo := newDiscoveryRepo(t)
	actorWorktreeGit(
		t,
		repo.Root,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"add",
		"-q",
		middle.Root,
		"dependencies/middle",
	)
	actorWorktreeGit(t, repo.Root, "commit", "-qam", "add middle submodule")
	actorWorktreeGit(
		t,
		repo.Root,
		"-c",
		"protocol.file.allow=always",
		"submodule",
		"update",
		"--init",
		"--recursive",
	)
	rootBaseline := actorWorktreeGitOutput(t, repo.Root, "rev-parse", "HEAD")
	primaryLeaf := filepath.Join(repo.Root, "dependencies/middle/dependencies/leaf")
	writeActorWorktreeFile(t, primaryLeaf, "dirty.txt", "primary dirty baseline\n")

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	for _, snapshot := range quarantine.Submodules {
		submoduleRepo, err := actorSubmoduleRepo(quarantine.Repo.Root, snapshot.Path)
		if err != nil {
			t.Fatal(err)
		}
		baselineRef, err := actorBaselineRef(quarantine.Repo.Root, snapshot.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok, err := readActorBaselinePin(submoduleRepo, baselineRef); err != nil || !ok || got != snapshot.BaselineTree {
			t.Fatalf("submodule %q baseline pin = %q present=%t err=%v, want %q", snapshot.Path, got, ok, err, snapshot.BaselineTree)
		}
		actorWorktreeGit(t, submoduleRepo.Root, "gc", "--prune=now")
	}

	quarantineLeaf := filepath.Join(
		quarantine.Repo.Root,
		"dependencies/middle/dependencies/leaf",
	)
	writeActorWorktreeFile(t, quarantineLeaf, "committed.txt", "actor committed change\n")
	actorWorktreeGit(t, quarantineLeaf, "add", "committed.txt")
	actorWorktreeGit(t, quarantineLeaf, "commit", "-qm", "actor leaf commit")
	actorHead := actorWorktreeGitOutput(t, quarantineLeaf, "rev-parse", "HEAD")
	writeActorWorktreeFile(t, quarantineLeaf, "dirty.txt", "actor uncommitted change\n")

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
	wantPaths := []string{
		"dependencies/middle/dependencies/leaf/committed.txt",
		"dependencies/middle/dependencies/leaf/dirty.txt",
	}
	if !slices.Equal(changedPaths, wantPaths) {
		t.Fatalf("nested submodule changed paths = %q, want %q", changedPaths, wantPaths)
	}
	if moved, err := quarantine.SubmoduleHeadsMoved(); err != nil || !moved {
		t.Fatalf("nested submodule HEAD movement: moved=%t err=%v", moved, err)
	}

	patch, err := quarantine.Patch(finalTree)
	if err != nil {
		t.Fatal(err)
	}
	if imported, err := quarantine.ImportSubmoduleChanges(); err != nil || !imported {
		t.Fatalf("import nested submodule changes: imported=%t err=%v", imported, err)
	}
	if imported, err := quarantine.ImportSubmoduleChanges(); err != nil || imported {
		t.Fatalf("idempotent nested submodule import: imported=%t err=%v", imported, err)
	}
	if _, err := repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil {
		t.Fatal(err)
	}

	if got := actorWorktreeGitOutput(t, primaryLeaf, "rev-parse", "HEAD"); got != actorHead {
		t.Fatalf("imported nested submodule HEAD = %q, want %q", got, actorHead)
	}
	if got := readActorWorktreeFile(t, primaryLeaf, "committed.txt"); got != "actor committed change\n" {
		t.Fatalf("imported committed nested file = %q", got)
	}
	if got := readActorWorktreeFile(t, primaryLeaf, "dirty.txt"); got != "actor uncommitted change\n" {
		t.Fatalf("imported uncommitted nested file = %q", got)
	}
	cumulativePaths, err := repo.ChangedFilesSinceRecursive(rootBaseline)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cumulativePaths, wantPaths) {
		t.Fatalf("recursive cumulative changed paths = %q, want %q", cumulativePaths, wantPaths)
	}

	if err := quarantine.Remove(); err != nil {
		t.Fatal(err)
	}
	actorWorktreeGit(t, primaryLeaf, "cat-file", "-e", actorHead+"^{commit}")
}

func actorWorktreeGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
