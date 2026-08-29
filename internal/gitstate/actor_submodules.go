package gitstate

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ActorSubmoduleSnapshot is the durable baseline for one initialized
// submodule in an actor quarantine. Path is relative to the superproject,
// including every parent submodule for nested repositories.
type ActorSubmoduleSnapshot struct {
	Path                string          `json:"path"`
	StartCommit         string          `json:"start_commit"`
	BaselineTree        string          `json:"baseline_tree"`
	BaselinePermissions FilePermissions `json:"baseline_permissions"`
}

type actorSubmoduleState struct {
	ActorSubmoduleSnapshot
	Repo        Repo
	Head        string
	FinalTree   string
	Permissions FilePermissions
}

func copyInitializedSubmoduleSnapshots(source, destination Repo) ([]ActorSubmoduleSnapshot, error) {
	return copyInitializedSubmoduleSnapshotsAt(source, destination, "", destination.Root)
}

func copyInitializedSubmoduleSnapshotsAt(source, destination Repo, parentPath, worktreeRoot string) ([]ActorSubmoduleSnapshot, error) {
	submodules, err := listInitializedSubmodules(source)
	if err != nil {
		return nil, err
	}

	snapshots := make([]ActorSubmoduleSnapshot, 0, len(submodules))
	for _, submodule := range submodules {
		sourceSubmodule, err := actorSubmoduleRepo(source.Root, submodule.path)
		if err != nil {
			return nil, fmt.Errorf("resolve initialized submodule %q: %w", submodule.path, err)
		}
		sourceRoot := sourceSubmodule.Root
		if err := rejectTrackedActorPrivatePaths(sourceSubmodule); err != nil {
			return nil, fmt.Errorf("validate initialized submodule %q actor view: %w", submodule.path, err)
		}
		if err := initializeActorSubmodule(destination, submodule, sourceRoot); err != nil {
			return nil, err
		}

		destinationSubmodule, err := actorSubmoduleRepo(destination.Root, submodule.path)
		if err != nil {
			return nil, fmt.Errorf("resolve quarantined submodule %q: %w", submodule.path, err)
		}
		startCommit, err := sourceSubmodule.Head()
		if err != nil {
			return nil, fmt.Errorf("inspect initialized submodule %q HEAD: %w", submodule.path, err)
		}
		if _, err := destinationSubmodule.run(
			nil,
			"-c",
			"protocol.file.allow=always",
			"fetch",
			"--no-tags",
			sourceRoot,
			startCommit,
		); err != nil {
			return nil, fmt.Errorf("copy initialized submodule %q objects: %w", submodule.path, err)
		}
		if _, err := destinationSubmodule.run(nil, "checkout", "--detach", "--force", startCommit); err != nil {
			return nil, fmt.Errorf("align initialized submodule %q HEAD: %w", submodule.path, err)
		}

		patch, err := sourceSubmodule.run(nil, "diff", "--binary", "--full-index", "HEAD", "--")
		if err != nil {
			return nil, fmt.Errorf("snapshot initialized submodule %q tracked changes: %w", submodule.path, err)
		}
		if len(patch) > 0 {
			if _, err := destinationSubmodule.run(patch, "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
				return nil, fmt.Errorf("copy initialized submodule %q tracked changes: %w", submodule.path, err)
			}
		}
		if err := removeActorPrivatePaths(destinationSubmodule.Root); err != nil {
			return nil, fmt.Errorf("remove initialized submodule %q private paths: %w", submodule.path, err)
		}
		if err := copyWorkspaceDirectories(sourceSubmodule, destinationSubmodule); err != nil {
			return nil, fmt.Errorf("copy initialized submodule %q directories: %w", submodule.path, err)
		}
		if err := copyUntrackedWorkspaceFiles(sourceSubmodule, destinationSubmodule); err != nil {
			return nil, fmt.Errorf("copy initialized submodule %q non-tracked files: %w", submodule.path, err)
		}
		baselinePermissions, err := snapshotFilePermissions(sourceSubmodule.Root)
		if err != nil {
			return nil, fmt.Errorf("snapshot initialized submodule %q baseline permissions: %w", submodule.path, err)
		}
		baselinePermissions = actorVisibleFilePermissions(baselinePermissions)
		if err := applyFilePermissions(destinationSubmodule.Root, baselinePermissions); err != nil {
			return nil, fmt.Errorf("copy initialized submodule %q permissions: %w", submodule.path, err)
		}

		path := joinActorSubmodulePath(parentPath, submodule.path)
		nested, err := copyInitializedSubmoduleSnapshotsAt(sourceSubmodule, destinationSubmodule, path, worktreeRoot)
		if err != nil {
			return nil, err
		}
		baselineTree, err := writeActorRepoTree(destinationSubmodule)
		if err != nil {
			return nil, fmt.Errorf("snapshot initialized submodule %q baseline: %w", path, err)
		}
		baselineRef, err := actorBaselineRef(worktreeRoot, path)
		if err != nil {
			return nil, fmt.Errorf("identify initialized submodule %q baseline: %w", path, err)
		}
		if err := createActorBaselinePin(destinationSubmodule, baselineRef, baselineTree); err != nil {
			return nil, fmt.Errorf("pin initialized submodule %q baseline: %w", path, err)
		}
		if _, err := destinationSubmodule.run(nil, "reset", "--mixed", "HEAD"); err != nil {
			return nil, fmt.Errorf("prepare initialized submodule %q workspace: %w", path, err)
		}

		snapshots = append(snapshots, ActorSubmoduleSnapshot{
			Path:                path,
			StartCommit:         startCommit,
			BaselineTree:        baselineTree,
			BaselinePermissions: baselinePermissions,
		})
		snapshots = append(snapshots, nested...)
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Path < snapshots[j].Path })
	return snapshots, nil
}

func initializeActorSubmodule(destination Repo, submodule initializedSubmodule, sourceRoot string) error {
	urlOverride := "submodule." + submodule.name + ".url=" + sourceRoot
	if _, err := destination.run(
		nil,
		"-c",
		"protocol.file.allow=always",
		"-c",
		urlOverride,
		"submodule",
		"update",
		"--init",
		"--checkout",
		"--no-fetch",
		"--",
		submodule.path,
	); err != nil {
		return fmt.Errorf("populate initialized submodule %q: %w", submodule.path, err)
	}
	return nil
}

func writeActorRepoTree(repo Repo) (string, error) {
	if _, err := repo.run(nil, "add", "-A", "--force", "--", "."); err != nil {
		return "", err
	}
	tree, err := repo.run(nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

func writeActorPolicyTree(repo Repo) (tree string, err error) {
	indexDir, err := os.MkdirTemp("", "agentflow-index-")
	if err != nil {
		return "", fmt.Errorf("create actor policy index: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(indexDir); err == nil && removeErr != nil {
			err = fmt.Errorf("remove actor policy index: %w", removeErr)
		}
	}()

	env := []string{"GIT_INDEX_FILE=" + filepath.Join(indexDir, "index")}
	if _, err := repo.runWithEnv(nil, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := repo.runWithEnv(nil, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	output, err := repo.runWithEnv(nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func joinActorSubmodulePath(parent, child string) string {
	if parent == "" {
		return filepath.ToSlash(filepath.Clean(child))
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(parent), filepath.FromSlash(child)))
}

func actorSubmoduleRepo(rootPath, submodulePath string) (Repo, error) {
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return Repo{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Repo{}, err
	}
	localPath := filepath.FromSlash(submodulePath)
	candidate := filepath.Join(root, localPath)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return Repo{}, err
	}
	if resolved != candidate {
		return Repo{}, fmt.Errorf("submodule path traverses a symbolic link")
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return Repo{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Repo{}, fmt.Errorf("submodule path is not a directory")
	}
	gitInfo, err := os.Lstat(filepath.Join(resolved, ".git"))
	if err != nil {
		return Repo{}, err
	}
	if gitInfo.Mode()&os.ModeSymlink != 0 || (!gitInfo.Mode().IsRegular() && !gitInfo.IsDir()) {
		return Repo{}, fmt.Errorf("submodule Git metadata has an unsafe file type")
	}
	return Repo{Root: resolved}, nil
}

func validateActorSubmoduleSnapshots(worktreeRoot string, snapshots []ActorSubmoduleSnapshot) error {
	seen := make(map[string]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		localPath := filepath.FromSlash(snapshot.Path)
		isGitPath := snapshot.Path == ".git" || strings.HasPrefix(snapshot.Path, ".git/")
		if snapshot.Path == "" || snapshot.Path == "." || filepath.IsAbs(localPath) || !filepath.IsLocal(localPath) || isGitPath || strings.ContainsRune(snapshot.Path, 0) {
			return fmt.Errorf("unsafe actor submodule path %q", snapshot.Path)
		}
		cleanPath := filepath.ToSlash(filepath.Clean(localPath))
		if cleanPath != snapshot.Path {
			return fmt.Errorf("unclean actor submodule path %q", snapshot.Path)
		}
		if snapshot.StartCommit == "" || snapshot.BaselineTree == "" {
			return fmt.Errorf("actor submodule %q baseline is incomplete", snapshot.Path)
		}
		if err := validateFilePermissions(snapshot.BaselinePermissions); err != nil {
			return fmt.Errorf("validate actor submodule %q baseline permissions: %w", snapshot.Path, err)
		}
		if _, duplicate := seen[snapshot.Path]; duplicate {
			return fmt.Errorf("duplicate actor submodule path %q", snapshot.Path)
		}
		seen[snapshot.Path] = struct{}{}

		repo, err := actorSubmoduleRepo(worktreeRoot, snapshot.Path)
		if err != nil {
			return fmt.Errorf("inspect actor submodule %q: %w", snapshot.Path, err)
		}
		if _, err := repo.run(nil, "cat-file", "-e", snapshot.StartCommit+"^{commit}"); err != nil {
			return fmt.Errorf("inspect actor submodule %q start commit: %w", snapshot.Path, err)
		}
		if _, err := repo.run(nil, "cat-file", "-e", snapshot.BaselineTree+"^{tree}"); err != nil {
			return fmt.Errorf("inspect actor submodule %q baseline tree: %w", snapshot.Path, err)
		}
	}
	return nil
}

func (w *ActorWorktree) snapshotFinalSubmodules() ([]actorSubmoduleState, error) {
	if err := validateActorSubmoduleSnapshots(w.Repo.Root, w.Submodules); err != nil {
		return nil, err
	}
	snapshots := append([]ActorSubmoduleSnapshot(nil), w.Submodules...)
	sort.Slice(snapshots, func(i, j int) bool {
		return actorSubmoduleDepth(snapshots[i].Path) > actorSubmoduleDepth(snapshots[j].Path)
	})

	states := make([]actorSubmoduleState, 0, len(snapshots))
	for _, snapshot := range snapshots {
		repo, err := actorSubmoduleRepo(w.Repo.Root, snapshot.Path)
		if err != nil {
			return nil, fmt.Errorf("inspect actor submodule %q: %w", snapshot.Path, err)
		}
		finalTree, err := writeActorRepoTree(repo)
		if err != nil {
			return nil, fmt.Errorf("snapshot actor submodule %q changes: %w", snapshot.Path, err)
		}
		permissions, err := snapshotFilePermissions(repo.Root)
		if err != nil {
			return nil, fmt.Errorf("snapshot actor submodule %q permissions: %w", snapshot.Path, err)
		}
		head, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("inspect actor submodule %q HEAD: %w", snapshot.Path, err)
		}
		states = append(states, actorSubmoduleState{
			ActorSubmoduleSnapshot: snapshot,
			Repo:                   repo,
			Head:                   head,
			FinalTree:              finalTree,
			Permissions:            permissions,
		})
	}
	return states, nil
}

// SubmoduleHeadsMoved reports whether an actor moved any initialized
// submodule HEAD and rejects rewritten submodule history.
func (w *ActorWorktree) SubmoduleHeadsMoved() (bool, error) {
	if err := validateActorSubmoduleSnapshots(w.Repo.Root, w.Submodules); err != nil {
		return false, err
	}
	moved := false
	for _, snapshot := range w.Submodules {
		repo, err := actorSubmoduleRepo(w.Repo.Root, snapshot.Path)
		if err != nil {
			return false, fmt.Errorf("inspect actor submodule %q: %w", snapshot.Path, err)
		}
		head, err := repo.Head()
		if err != nil {
			return false, fmt.Errorf("inspect actor submodule %q HEAD: %w", snapshot.Path, err)
		}
		if head == snapshot.StartCommit {
			continue
		}
		moved = true
		if !repo.IsAncestor(snapshot.StartCommit, head) {
			return false, fmt.Errorf("repository policy: actor moved submodule %q HEAD outside the invocation lineage", snapshot.Path)
		}
	}
	return moved, nil
}

func actorSubmoduleDepth(path string) int {
	return strings.Count(path, "/") + 1
}

func (w *ActorWorktree) directSubmodulePaths(parentPath string) []string {
	paths := make([]string, 0)
	for _, snapshot := range w.Submodules {
		if actorSubmoduleParent(snapshot.Path, w.Submodules) != parentPath {
			continue
		}
		if parentPath == "" {
			paths = append(paths, snapshot.Path)
			continue
		}
		paths = append(paths, strings.TrimPrefix(snapshot.Path, parentPath+"/"))
	}
	sort.Strings(paths)
	return paths
}

func actorSubmoduleParent(path string, snapshots []ActorSubmoduleSnapshot) string {
	parent := ""
	for _, candidate := range snapshots {
		if candidate.Path == path || !strings.HasPrefix(path, candidate.Path+"/") {
			continue
		}
		if len(candidate.Path) > len(parent) {
			parent = candidate.Path
		}
	}
	return parent
}

// ChangedFilesSinceRecursive expands changed initialized-submodule gitlinks
// into the paths that changed inside those repositories. A gitlink remains in
// the result when its commit moved without changing any visible path, or when
// the submodule cannot be related to the requested baseline.
func (r Repo) ChangedFilesSinceRecursive(base string) ([]string, error) {
	changed := make(map[string]struct{})
	if err := changedFilesSinceRecursive(r, base, "", changed); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func changedFilesSinceRecursive(repo Repo, base, prefix string, changed map[string]struct{}) error {
	policyTree, err := writeActorPolicyTree(repo)
	if err != nil {
		return err
	}
	output, err := repo.run(nil, "diff", "--name-only", "-z", base, policyTree, "--", ".")
	if err != nil {
		return err
	}
	for _, path := range bytes.Split(output, []byte{0}) {
		if len(path) != 0 {
			changed[joinActorSubmodulePath(prefix, string(path))] = struct{}{}
		}
	}

	submodules, err := listInitializedSubmodules(repo)
	if err != nil {
		return err
	}
	for _, submodule := range submodules {
		baseline, ok, err := gitlinkCommitAt(repo, base, submodule.path)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		submoduleRepo, err := actorSubmoduleRepo(repo.Root, submodule.path)
		if err != nil {
			return fmt.Errorf("inspect initialized submodule %q: %w", joinActorSubmodulePath(prefix, submodule.path), err)
		}
		submoduleChanges := make(map[string]struct{})
		submodulePrefix := joinActorSubmodulePath(prefix, submodule.path)
		if err := changedFilesSinceRecursive(submoduleRepo, baseline, submodulePrefix, submoduleChanges); err != nil {
			return fmt.Errorf("inspect initialized submodule %q changes: %w", submodulePrefix, err)
		}
		if len(submoduleChanges) == 0 {
			continue
		}
		delete(changed, submodulePrefix)
		for path := range submoduleChanges {
			changed[path] = struct{}{}
		}
	}
	return nil
}

func gitlinkCommitAt(repo Repo, treeish, path string) (string, bool, error) {
	output, err := repo.run(nil, "ls-tree", "-z", treeish, "--", path)
	if err != nil {
		return "", false, fmt.Errorf("inspect submodule %q at %s: %w", path, treeish, err)
	}
	entry := bytes.TrimSuffix(output, []byte{0})
	metadata, entryPath, found := bytes.Cut(entry, []byte{'\t'})
	if !found || string(entryPath) != path {
		return "", false, nil
	}
	fields := strings.Fields(string(metadata))
	if len(fields) != 3 || fields[0] != "160000" || fields[1] != "commit" {
		return "", false, nil
	}
	return fields[2], true, nil
}

func actorRepoDiff(repo Repo, baselineTree, finalTree string, excludedPaths []string, nameOnly bool) ([]byte, error) {
	args := []string{"diff"}
	if nameOnly {
		args = append(args, "--name-only", "-z")
	} else {
		args = append(args, "--binary", "--full-index")
	}
	args = append(args, baselineTree, finalTree, "--", ".")
	for _, path := range excludedPaths {
		args = append(args, ":(exclude,literal)"+path)
	}
	return repo.run(nil, args...)
}

func parseActorChangedPaths(output []byte, prefix string) ([]string, error) {
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		path := string(part)
		if filepath.IsAbs(path) || !filepath.IsLocal(path) {
			return nil, fmt.Errorf("unsafe actor quarantine path %q", path)
		}
		paths = append(paths, joinActorSubmodulePath(prefix, path))
	}
	return paths, nil
}

func (w *ActorWorktree) submoduleChangedPaths(states []actorSubmoduleState) ([]string, error) {
	changed := make(map[string]struct{})
	for _, state := range states {
		excluded := w.directSubmodulePaths(state.Path)
		output, err := actorRepoDiff(
			state.Repo,
			state.BaselineTree,
			state.FinalTree,
			excluded,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("inspect actor submodule %q changes: %w", state.Path, err)
		}
		paths, err := parseActorChangedPaths(output, state.Path)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			changed[path] = struct{}{}
		}
		for path, baselineMode := range state.BaselinePermissions {
			if finalMode, ok := state.Permissions[path]; ok && finalMode != baselineMode {
				changed[joinActorSubmodulePath(state.Path, path)] = struct{}{}
			}
		}
		for path := range state.Permissions {
			if _, existed := state.BaselinePermissions[path]; !existed {
				changed[joinActorSubmodulePath(state.Path, path)] = struct{}{}
			}
		}
	}

	for _, state := range states {
		if state.Head == state.StartCommit || actorSubmoduleHasChangedContent(state.Path, changed) {
			continue
		}
		changed[state.Path] = struct{}{}
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func actorSubmoduleHasChangedContent(submodulePath string, changed map[string]struct{}) bool {
	prefix := submodulePath + "/"
	for path := range changed {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// ImportSubmoduleChanges recursively imports actor worktree deltas and commit
// objects into their matching initialized authoritative submodules. Deepest
// repositories are imported first so parent gitlinks observe their final HEAD.
func (w *ActorWorktree) ImportSubmoduleChanges() (bool, error) {
	states, err := w.snapshotFinalSubmodules()
	if err != nil {
		return false, err
	}
	applied := false
	for _, state := range states {
		primaryRepo, err := actorSubmoduleRepo(w.Primary.Root, state.Path)
		if err != nil {
			return false, fmt.Errorf("inspect authoritative submodule %q: %w", state.Path, err)
		}
		primaryHead, err := primaryRepo.Head()
		if err != nil {
			return false, fmt.Errorf("inspect authoritative submodule %q HEAD: %w", state.Path, err)
		}
		if primaryHead != state.StartCommit && primaryHead != state.Head {
			return false, fmt.Errorf("authoritative submodule %q HEAD changed during the actor invocation", state.Path)
		}

		headMoved := state.Head != state.StartCommit
		if headMoved && !state.Repo.IsAncestor(state.StartCommit, state.Head) {
			return false, fmt.Errorf("repository policy: actor moved submodule %q HEAD outside the invocation lineage", state.Path)
		}
		if headMoved && !primaryRepo.ObjectExists(state.Head) {
			if _, err := primaryRepo.run(
				nil,
				"-c",
				"protocol.file.allow=always",
				"fetch",
				"--no-tags",
				state.Repo.Root,
				state.Head,
			); err != nil {
				return false, fmt.Errorf("import actor submodule %q commit objects: %w", state.Path, err)
			}
		}

		patch, err := actorRepoDiff(
			state.Repo,
			state.BaselineTree,
			state.FinalTree,
			w.directSubmodulePaths(state.Path),
			false,
		)
		if err != nil {
			return false, fmt.Errorf("build actor submodule %q import: %w", state.Path, err)
		}
		changed, err := primaryRepo.ApplyPatchIdempotent(patch, state.Permissions)
		if err != nil {
			return false, fmt.Errorf("import actor submodule %q changes: %w", state.Path, err)
		}
		applied = applied || changed
		if headMoved && primaryHead != state.Head {
			if err := primaryRepo.AdoptActorHead(state.Head); err != nil {
				return false, fmt.Errorf("adopt actor submodule %q HEAD: %w", state.Path, err)
			}
			applied = true
		}
	}
	return applied, nil
}
