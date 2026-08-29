package gitstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	actorQuarantineDirectoryPrefix       = "actor-quarantine-"
	actorQuarantineRootDirectory         = ".actor-quarantines"
	legacyActorQuarantineDirectoryPrefix = "agentflow-quarantine-"
	legacyActorQuarantineRootDirectory   = ".agentflow-quarantines"
	actorWorktreeDirectoryName           = "worktree"
)

// ActorWorktree is a detached, disposable checkout used to isolate one actor
// invocation from the workflow's authoritative workspace.
type ActorWorktree struct {
	Primary             Repo
	Repo                Repo
	Parent              string
	StartCommit         string
	BaselineTree        string
	BaselinePermissions FilePermissions
	Submodules          []ActorSubmoduleSnapshot
}

// FilePermissions records the complete permission bits for regular workspace
// files. Git trees retain only the executable bit, so this metadata travels
// alongside tree snapshots when exact filesystem state matters.
type FilePermissions map[string]uint32

// CreateActorWorktree creates a detached worktree whose files match the
// primary workspace, including tracked dirt and ignored or untracked files.
// The returned baseline tree makes later imports relative to the exact state
// the actor observed rather than merely to HEAD.
func (r Repo) CreateActorWorktree() (_ *ActorWorktree, returnErr error) {
	return r.createActorWorktree(ensureActorQuarantineRoot)
}

func (r Repo) createActorWorktree(resolveRoot func(Repo) (string, error)) (_ *ActorWorktree, returnErr error) {
	if err := rejectTrackedActorPrivatePaths(r); err != nil {
		return nil, err
	}
	start, err := r.Head()
	if err != nil {
		return nil, err
	}
	quarantineRoot, err := resolveRoot(r)
	if err != nil {
		return nil, err
	}
	parent, err := os.MkdirTemp(quarantineRoot, actorQuarantineDirectoryPrefix)
	if err != nil {
		return nil, fmt.Errorf("create actor quarantine directory: %w", err)
	}
	path := filepath.Join(parent, actorWorktreeDirectoryName)
	worktree := &ActorWorktree{
		Primary:     r,
		Repo:        Repo{Root: path},
		Parent:      parent,
		StartCommit: start,
	}
	defer func() {
		if returnErr != nil {
			if err := worktree.Remove(); err != nil {
				// Remove only empty directories when Git never registered the
				// worktree. Any populated quarantine is retained for inspection.
				_ = os.Remove(path)
				_ = os.Remove(parent)
			}
		}
	}()

	if _, err := r.run(nil, "worktree", "add", "--detach", "--no-checkout", path, start); err != nil {
		return nil, fmt.Errorf("create actor quarantine worktree: %w", err)
	}
	if _, err := worktree.Repo.run(nil, "checkout", "--force", start, "--"); err != nil {
		return nil, fmt.Errorf("populate actor quarantine worktree: %w", err)
	}
	patch, err := r.run(nil, "diff", "--binary", "--full-index", "HEAD", "--")
	if err != nil {
		return nil, fmt.Errorf("snapshot primary tracked changes: %w", err)
	}
	if len(patch) > 0 {
		if _, err := worktree.Repo.run(patch, "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
			return nil, fmt.Errorf("copy primary tracked changes into actor quarantine: %w", err)
		}
	}
	if err := removeActorPrivatePaths(worktree.Repo.Root); err != nil {
		return nil, err
	}
	submodules, err := copyInitializedSubmoduleSnapshots(r, worktree.Repo)
	if err != nil {
		return nil, err
	}
	worktree.Submodules = submodules
	if err := copyWorkspaceDirectories(r, worktree.Repo); err != nil {
		return nil, err
	}
	if err := copyUntrackedWorkspaceFiles(r, worktree.Repo); err != nil {
		return nil, err
	}
	if err := validateActorWorktreeSymlinks(worktree.Repo.Root); err != nil {
		return nil, err
	}
	baselinePermissions, err := snapshotFilePermissions(r.Root)
	if err != nil {
		return nil, fmt.Errorf("snapshot actor quarantine baseline permissions: %w", err)
	}
	baselinePermissions = actorVisibleFilePermissions(baselinePermissions)
	if err := applyFilePermissions(worktree.Repo.Root, baselinePermissions); err != nil {
		return nil, fmt.Errorf("copy primary permissions into actor quarantine: %w", err)
	}
	baseline, err := worktree.writeTree()
	if err != nil {
		return nil, fmt.Errorf("snapshot actor quarantine baseline: %w", err)
	}
	worktree.BaselineTree = baseline
	worktree.BaselinePermissions = baselinePermissions
	baselineRef, err := actorBaselineRef(worktree.Repo.Root, "")
	if err != nil {
		return nil, fmt.Errorf("identify actor quarantine baseline: %w", err)
	}
	if err := createActorBaselinePin(worktree.Repo, baselineRef, baseline); err != nil {
		return nil, err
	}
	if _, err := worktree.Repo.run(nil, "reset", "--mixed", "HEAD"); err != nil {
		return nil, fmt.Errorf("prepare actor quarantine workspace: %w", err)
	}
	return worktree, nil
}

// RecoverActorWorktree reconstructs an actor worktree from durable state only
// after verifying that it has the layout of a runtime-created quarantine.
func RecoverActorWorktree(primary Repo, path, startCommit, baselineTree string, baselinePermissions FilePermissions, submodules []ActorSubmoduleSnapshot) (*ActorWorktree, error) {
	parent, err := actorQuarantineParent(primary, path)
	if err != nil {
		return nil, err
	}
	worktree := &ActorWorktree{
		Primary:             primary,
		Repo:                Repo{Root: path},
		Parent:              parent,
		StartCommit:         startCommit,
		BaselineTree:        baselineTree,
		BaselinePermissions: baselinePermissions,
		Submodules:          append([]ActorSubmoduleSnapshot(nil), submodules...),
	}
	if err := validateFilePermissions(baselinePermissions); err != nil {
		return nil, fmt.Errorf("validate actor quarantine baseline permissions: %w", err)
	}
	if err := validateActorSubmoduleSnapshots(path, submodules); err != nil {
		return nil, fmt.Errorf("validate actor quarantine submodules: %w", err)
	}
	if err := worktree.validateCleanupPaths(); err != nil {
		return nil, err
	}
	if err := worktree.validateBaselinePins(); err != nil {
		return nil, fmt.Errorf("validate actor quarantine baseline pins: %w", err)
	}
	return worktree, nil
}

func actorQuarantineRoot(repo Repo) (string, error) {
	mainWorktree, repositoryID, err := actorQuarantineIdentity(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(mainWorktree), actorQuarantineRootDirectory, repositoryID), nil
}

func actorQuarantineIdentity(repo Repo) (string, string, error) {
	if repo.Root == "" {
		return "", "", fmt.Errorf("resolve actor quarantine root: repository root is empty")
	}
	output, err := repo.run(nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("resolve actor quarantine root: %w", err)
	}
	commonDir := strings.TrimSpace(string(output))
	if commonDir == "" {
		return "", "", fmt.Errorf("resolve actor quarantine root: Git common directory is empty")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo.Root, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve actor quarantine root: %w", err)
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve actor quarantine root: %w", err)
	}

	worktrees, err := repo.run(nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return "", "", fmt.Errorf("resolve actor quarantine root: %w", err)
	}
	fields := bytes.Split(worktrees, []byte{0})
	const worktreePrefix = "worktree "
	if len(fields) == 0 || !bytes.HasPrefix(fields[0], []byte(worktreePrefix)) {
		return "", "", fmt.Errorf("resolve actor quarantine root: primary worktree is missing")
	}
	mainWorktree := string(bytes.TrimPrefix(fields[0], []byte(worktreePrefix)))
	if !filepath.IsAbs(mainWorktree) {
		return "", "", fmt.Errorf("resolve actor quarantine root: primary worktree path is not absolute")
	}
	mainWorktree, err = filepath.EvalSymlinks(mainWorktree)
	if err != nil {
		return "", "", fmt.Errorf("resolve actor quarantine root: %w", err)
	}
	digest := sha256.Sum256([]byte(commonDir))
	repositoryID := hex.EncodeToString(digest[:16])
	return mainWorktree, repositoryID, nil
}

func actorQuarantineFallbackRoot(repo Repo) (string, error) {
	mainWorktree, repositoryID, err := actorQuarantineIdentity(repo)
	if err != nil {
		return "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve actor quarantine fallback root: %w", err)
	}
	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		return "", fmt.Errorf("resolve actor quarantine fallback root: %w", err)
	}
	if resolvedCacheDir, err := filepath.EvalSymlinks(cacheDir); err == nil {
		cacheDir = resolvedCacheDir
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("resolve actor quarantine fallback root: %w", err)
	}
	fallbackRoot := filepath.Join(cacheDir, actorQuarantineRootDirectory, repositoryID)
	for _, workspace := range []string{mainWorktree, repo.Root} {
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			return "", fmt.Errorf("resolve actor quarantine fallback root: %w", err)
		}
		if resolvedWorkspace, err := filepath.EvalSymlinks(workspace); err == nil {
			workspace = resolvedWorkspace
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve actor quarantine fallback root: %w", err)
		}
		if pathIsWithin(workspace, fallbackRoot) {
			return "", fmt.Errorf("resolve actor quarantine fallback root: cache directory is inside authoritative workspace")
		}
	}
	return fallbackRoot, nil
}

func pathIsWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

type actorQuarantineRootUnavailableError struct {
	root string
	err  error
}

func (e *actorQuarantineRootUnavailableError) Error() string {
	return fmt.Sprintf("actor quarantine root %q is unavailable: %v", e.root, e.err)
}

func (e *actorQuarantineRootUnavailableError) Unwrap() error {
	return e.err
}

func actorQuarantineRootUnavailable(root string, err error) error {
	return &actorQuarantineRootUnavailableError{root: root, err: err}
}

func ensureActorQuarantineRoot(repo Repo) (string, error) {
	return ensureActorQuarantineRootWith(repo, initializeActorQuarantineRoot)
}

func ensureActorQuarantineRootWith(repo Repo, initialize func(string) error) (string, error) {
	if initialize == nil {
		return "", fmt.Errorf("create actor quarantine root: initializer is nil")
	}
	preferredRoot, err := actorQuarantineRoot(repo)
	if err != nil {
		return "", err
	}
	preferredErr := initialize(preferredRoot)
	if preferredErr == nil {
		return preferredRoot, nil
	}
	var unavailable *actorQuarantineRootUnavailableError
	if !errors.As(preferredErr, &unavailable) {
		return "", preferredErr
	}

	fallbackRoot, err := actorQuarantineFallbackRoot(repo)
	if err != nil {
		return "", fmt.Errorf("%v; resolve fallback: %w", preferredErr, err)
	}
	cacheDir := filepath.Dir(filepath.Dir(fallbackRoot))
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("%v; create actor quarantine cache directory: %w", preferredErr, err)
	}
	// Resolve the cache path again after creation so a newly resolvable
	// symlink cannot make the durable path differ from the cleanup authority.
	fallbackRoot, err = actorQuarantineFallbackRoot(repo)
	if err != nil {
		return "", fmt.Errorf("%v; resolve created fallback: %w", preferredErr, err)
	}
	if err := initialize(fallbackRoot); err != nil {
		return "", fmt.Errorf("preferred actor quarantine root failed: %v; fallback failed: %w", preferredErr, err)
	}
	return fallbackRoot, nil
}

func initializeActorQuarantineRoot(root string) error {
	anchor := filepath.Dir(filepath.Dir(root))
	anchorRoot, err := os.OpenRoot(anchor)
	if err != nil {
		return actorQuarantineRootUnavailable(root, fmt.Errorf("open actor quarantine anchor: %w", err))
	}
	defer anchorRoot.Close()
	baseName := filepath.Base(filepath.Dir(root))
	if err := anchorRoot.Mkdir(baseName, 0o700); err != nil && !os.IsExist(err) {
		return actorQuarantineRootUnavailable(root, fmt.Errorf("create actor quarantine parent: %w", err))
	}
	if err := validateActorQuarantineDirectory("parent", filepath.Dir(root)); err != nil {
		return err
	}
	baseRoot, err := anchorRoot.OpenRoot(baseName)
	if err != nil {
		return actorQuarantineRootUnavailable(root, fmt.Errorf("open actor quarantine parent: %w", err))
	}
	defer baseRoot.Close()
	if err := baseRoot.Mkdir(filepath.Base(root), 0o700); err != nil && !os.IsExist(err) {
		return actorQuarantineRootUnavailable(root, fmt.Errorf("create actor quarantine root: %w", err))
	}
	if err := validateActorQuarantineRoot(root); err != nil {
		return err
	}
	return nil
}

func validateActorQuarantineRoot(root string) error {
	parent := filepath.Dir(root)
	for _, candidate := range []struct {
		label string
		path  string
	}{
		{label: "parent", path: parent},
		{label: "root", path: root},
	} {
		if err := validateActorQuarantineDirectory(candidate.label, candidate.path); err != nil {
			return err
		}
	}
	return nil
}

func validateActorQuarantineDirectory(label, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect actor quarantine %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("actor quarantine %s is not a directory", label)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("actor quarantine %s permissions %#o are not private", label, info.Mode().Perm())
	}
	return nil
}

func parseActorQuarantineParent(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("invalid actor quarantine path")
	}
	if filepath.Base(path) != actorWorktreeDirectoryName {
		return "", fmt.Errorf("invalid actor quarantine path")
	}

	parent := filepath.Dir(path)
	name := filepath.Base(parent)
	if !actorQuarantineDirectoryNameValid(name) {
		return "", fmt.Errorf("invalid actor quarantine path")
	}
	return parent, nil
}

func actorQuarantineDirectoryNameValid(name string) bool {
	for _, prefix := range []string{actorQuarantineDirectoryPrefix, legacyActorQuarantineDirectoryPrefix} {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

func actorQuarantineParent(primary Repo, path string) (string, error) {
	parent, err := parseActorQuarantineParent(path)
	if err != nil {
		return "", err
	}
	preferredRoot, err := actorQuarantineRoot(primary)
	if err != nil {
		return "", err
	}
	if actorQuarantineParentIsWithinRoot(preferredRoot, parent) {
		if err := validateActorQuarantineRoot(preferredRoot); err != nil {
			return "", err
		}
		return parent, nil
	}
	if err := validateRecordedActorQuarantineRoot(primary, filepath.Dir(parent)); err == nil {
		return parent, nil
	}
	return "", fmt.Errorf("invalid actor quarantine path")
}

// validateRecordedActorQuarantineRoot authorizes a fallback root from its
// durable worktree path. Recovery cannot rely on os.UserCacheDir because its
// result may change between creation and a later process restart.
func validateRecordedActorQuarantineRoot(primary Repo, root string) error {
	mainWorktree, repositoryID, err := actorQuarantineIdentity(primary)
	if err != nil {
		return err
	}
	rootDirectory := filepath.Base(filepath.Dir(root))
	if filepath.Base(root) != repositoryID ||
		(rootDirectory != actorQuarantineRootDirectory && rootDirectory != legacyActorQuarantineRootDirectory) {
		return fmt.Errorf("invalid actor quarantine path")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil || resolvedRoot != root {
		return fmt.Errorf("invalid actor quarantine path")
	}
	for _, workspace := range []string{mainWorktree, primary.Root} {
		workspace, err = filepath.Abs(workspace)
		if err != nil {
			return err
		}
		workspace, err = filepath.EvalSymlinks(workspace)
		if err != nil {
			return err
		}
		if pathIsWithin(workspace, root) {
			return fmt.Errorf("invalid actor quarantine path")
		}
	}
	return validateActorQuarantineRoot(root)
}

func actorQuarantineParentIsWithinRoot(root, parent string) bool {
	relativeParent, err := filepath.Rel(root, parent)
	if err != nil || relativeParent == "." || !filepath.IsLocal(relativeParent) || filepath.Dir(relativeParent) != "." {
		return false
	}
	return true
}

func (w *ActorWorktree) validateCleanupPaths() error {
	if w.Primary.Root == "" || w.Repo.Root == "" || w.Parent == "" {
		return fmt.Errorf("actor quarantine cleanup paths are incomplete")
	}
	parent, err := actorQuarantineParent(w.Primary, w.Repo.Root)
	if err != nil {
		return err
	}
	if w.Parent != parent {
		return fmt.Errorf("actor quarantine parent does not match worktree path")
	}
	paths := []struct {
		label string
		path  string
	}{
		{label: "parent", path: w.Parent},
		{label: "worktree", path: w.Repo.Root},
	}
	for _, candidate := range paths {
		info, err := os.Lstat(candidate.path)
		if err != nil {
			return fmt.Errorf("inspect actor quarantine %s: %w", candidate.label, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("actor quarantine %s is not a directory", candidate.label)
		}
	}
	return nil
}

type initializedSubmodule struct {
	name string
	path string
}

func listInitializedSubmodules(repo Repo) ([]initializedSubmodule, error) {
	modulesFile := filepath.Join(repo.Root, ".gitmodules")
	if _, err := os.Stat(modulesFile); err != nil {
		if os.IsNotExist(err) {
			return []initializedSubmodule{}, nil
		}
		return nil, fmt.Errorf("inspect submodule configuration: %w", err)
	}
	keys, err := repo.run(nil, "config", "--null", "--file", modulesFile, "--name-only", "--list")
	if err != nil {
		return nil, fmt.Errorf("list submodule configuration: %w", err)
	}

	const keyPrefix = "submodule."
	const keySuffix = ".path"
	submodules := []initializedSubmodule{}
	for _, rawKey := range bytes.Split(keys, []byte{0}) {
		key := string(rawKey)
		lowerKey := strings.ToLower(key)
		if !strings.HasPrefix(lowerKey, keyPrefix) || !strings.HasSuffix(lowerKey, keySuffix) {
			continue
		}
		name := key[len(keyPrefix) : len(key)-len(keySuffix)]
		pathOutput, err := repo.run(nil, "config", "--null", "--file", modulesFile, "--get", key)
		if err != nil {
			return nil, fmt.Errorf("read submodule path for %q: %w", name, err)
		}
		path := strings.TrimSuffix(string(pathOutput), "\x00")
		localPath := filepath.FromSlash(path)
		isGitPath := path == ".git" || strings.HasPrefix(path, ".git/")
		if name == "" || path == "" || path == "." || filepath.IsAbs(localPath) || !filepath.IsLocal(localPath) || isGitPath {
			return nil, fmt.Errorf("unsafe submodule path %q", path)
		}
		if strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("invalid submodule path %q", path)
		}
		if _, err := os.Lstat(filepath.Join(repo.Root, localPath, ".git")); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect initialized submodule %q: %w", path, err)
		}
		submodules = append(submodules, initializedSubmodule{name: name, path: path})
	}
	sort.Slice(submodules, func(i, j int) bool {
		return submodules[i].path < submodules[j].path
	})
	return submodules, nil
}

func copyWorkspaceDirectories(source, destination Repo) error {
	return filepath.WalkDir(source.Root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source.Root, path)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if IsActorPrivatePath(relative) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == "." || !entry.IsDir() {
			return nil
		}
		return os.MkdirAll(filepath.Join(destination.Root, relative), 0o755)
	})
}

func copyUntrackedWorkspaceFiles(source, destination Repo) error {
	paths, err := source.collectPaths(
		[]string{"ls-files", "--others", "--exclude-standard"},
		[]string{"ls-files", "--others", "--ignored", "--exclude-standard"},
	)
	if err != nil {
		return fmt.Errorf("list non-tracked workspace files: %w", err)
	}
	for _, path := range paths {
		if IsActorPrivatePath(path) {
			continue
		}
		if path == "" || filepath.IsAbs(path) || !filepath.IsLocal(path) {
			return fmt.Errorf("unsafe non-tracked workspace path %q", path)
		}
		from := filepath.Join(source.Root, filepath.FromSlash(path))
		to := filepath.Join(destination.Root, filepath.FromSlash(path))
		if err := copyWorkspaceFile(from, to); err != nil {
			return fmt.Errorf("copy non-tracked workspace path %q: %w", path, err)
		}
	}
	return nil
}

func copyWorkspaceFile(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		if err := os.Remove(to); err != nil && !os.IsNotExist(err) {
			return err
		}
		return os.Symlink(target, to)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported file mode %s", info.Mode())
	}
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(to, info.Mode().Perm())
}

func validateActorWorktreeSymlinks(rootPath string) error {
	rootPath, err := filepath.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("resolve actor quarantine path: %w", err)
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return fmt.Errorf("resolve actor quarantine path: %w", err)
	}

	err = filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		targetPath := target
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(filepath.Dir(path), targetPath)
		}
		if err := validateActorSymlinkTarget(rootPath, targetPath); err != nil {
			relative, relErr := filepath.Rel(rootPath, path)
			if relErr != nil {
				relative = path
			}
			return fmt.Errorf("unsafe actor quarantine baseline symlink %q targeting %q: %w", filepath.ToSlash(relative), target, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("validate actor quarantine baseline symlinks: %w", err)
	}
	return nil
}

func validateActorSymlinkTarget(rootPath, targetPath string) error {
	const maxSymlinkTraversals = 255

	targetPath = filepath.Clean(targetPath)
	for traversals := 0; ; {
		relative, err := filepath.Rel(rootPath, targetPath)
		if err != nil {
			return fmt.Errorf("resolve target relative to quarantine: %w", err)
		}
		if relative == "." {
			return nil
		}
		if !filepath.IsLocal(relative) {
			return fmt.Errorf("target escapes actor quarantine")
		}

		components := strings.Split(relative, string(filepath.Separator))
		current := rootPath
		followed := false
		for i, component := range components {
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				if os.IsNotExist(err) {
					// A missing suffix is a broken but contained link. It cannot
					// escape unless the actor first adds another symlink.
					return nil
				}
				return fmt.Errorf("inspect target component: %w", err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}

			traversals++
			if traversals > maxSymlinkTraversals {
				return fmt.Errorf("too many symlink traversals")
			}
			target, err := os.Readlink(current)
			if err != nil {
				return fmt.Errorf("read target component: %w", err)
			}
			if filepath.IsAbs(target) {
				targetPath = target
			} else {
				targetPath = filepath.Join(filepath.Dir(current), target)
			}
			if i+1 < len(components) {
				targetPath = filepath.Join(append([]string{targetPath}, components[i+1:]...)...)
			}
			targetPath = filepath.Clean(targetPath)
			followed = true
			break
		}
		if !followed {
			return nil
		}
	}
}

func snapshotFilePermissions(rootPath string) (FilePermissions, error) {
	permissions := FilePermissions{}
	err := filepath.WalkDir(rootPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(rootPath, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported workspace file mode %s at %q", info.Mode(), filepath.ToSlash(relative))
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("unsupported special permission bits %s at %q", info.Mode(), filepath.ToSlash(relative))
		}
		permissions[filepath.ToSlash(relative)] = uint32(info.Mode().Perm())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return permissions, nil
}

func validateFilePermissions(permissions FilePermissions) error {
	if permissions == nil {
		return fmt.Errorf("file permissions are missing")
	}
	for path, mode := range permissions {
		if path == "" || path == "." || filepath.IsAbs(path) || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
			return fmt.Errorf("unsafe file permission path %q", path)
		}
		if mode&^uint32(os.ModePerm) != 0 {
			return fmt.Errorf("unsupported file permissions %04o for %q", mode, path)
		}
		if mode&0o600 == 0 {
			return fmt.Errorf("file permissions %04o for %q deny safe owner access", mode, path)
		}
	}
	return nil
}

func applyFilePermissions(rootPath string, permissions FilePermissions) error {
	if err := validateFilePermissions(permissions); err != nil {
		return err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()

	paths := make([]string, 0, len(permissions))
	for path := range permissions {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		localPath := filepath.FromSlash(path)
		info, err := root.Lstat(localPath)
		if err != nil {
			return fmt.Errorf("inspect permission target %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("permission target %q is not a regular file", path)
		}
		file, openErr := root.OpenFile(localPath, os.O_RDONLY, 0)
		if openErr != nil {
			file, err = root.OpenFile(localPath, os.O_WRONLY, 0)
			if err != nil {
				return fmt.Errorf("open permission target %q: read: %v; write: %w", path, openErr, err)
			}
		}
		openedInfo, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return fmt.Errorf("inspect opened permission target %q: %w", path, err)
		}
		if !openedInfo.Mode().IsRegular() {
			_ = file.Close()
			return fmt.Errorf("opened permission target %q is not a regular file", path)
		}
		if err := file.Chmod(os.FileMode(permissions[path])); err != nil {
			_ = file.Close()
			return fmt.Errorf("restore permissions for %q: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close permission target %q: %w", path, err)
		}
	}
	return nil
}

func (w *ActorWorktree) writeTree() (string, error) {
	return writeActorRepoTree(w.Repo)
}

// FinalTree stages the quarantined checkout and returns its complete content
// tree. Staging is confined to the quarantine index.
func (w *ActorWorktree) FinalTree() (string, error) {
	if _, err := w.snapshotFinalSubmodules(); err != nil {
		return "", err
	}
	return w.writeTree()
}

// FilePermissions snapshots permission bits that cannot be represented by a
// Git tree. Special permission bits and non-regular files fail closed.
func (w *ActorWorktree) FilePermissions() (FilePermissions, error) {
	return snapshotFilePermissions(w.Repo.Root)
}

// Patch returns a binary-safe actor delta between the captured baseline and a
// final quarantine tree.
func (w *ActorWorktree) Patch(finalTree string) ([]byte, error) {
	if w.BaselineTree == "" || finalTree == "" {
		return nil, fmt.Errorf("actor quarantine tree is incomplete")
	}
	return actorRepoDiff(w.Repo, w.BaselineTree, finalTree, w.directSubmodulePaths(""), false)
}

// ChangedPaths returns the repository-relative paths changed by the actor.
func (w *ActorWorktree) ChangedPaths(finalTree string, finalPermissions FilePermissions) ([]string, error) {
	if w.BaselineTree == "" || finalTree == "" {
		return nil, fmt.Errorf("actor quarantine tree is incomplete")
	}
	if err := validateFilePermissions(w.BaselinePermissions); err != nil {
		return nil, fmt.Errorf("validate actor quarantine baseline permissions: %w", err)
	}
	if err := validateFilePermissions(finalPermissions); err != nil {
		return nil, fmt.Errorf("validate actor quarantine final permissions: %w", err)
	}
	output, err := actorRepoDiff(w.Repo, w.BaselineTree, finalTree, w.directSubmodulePaths(""), true)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(output, []byte{0})
	changed := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		path := string(part)
		if filepath.IsAbs(path) || !filepath.IsLocal(path) {
			return nil, fmt.Errorf("unsafe actor quarantine path %q", path)
		}
		changed[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}
	for path, baselineMode := range w.BaselinePermissions {
		if finalMode, ok := finalPermissions[path]; ok && finalMode != baselineMode {
			changed[path] = struct{}{}
		}
	}
	for path := range finalPermissions {
		if _, existed := w.BaselinePermissions[path]; !existed {
			changed[path] = struct{}{}
		}
	}
	states, err := w.snapshotFinalSubmodules()
	if err != nil {
		return nil, err
	}
	submodulePaths, err := w.submoduleChangedPaths(states)
	if err != nil {
		return nil, err
	}
	for _, path := range submodulePaths {
		changed[path] = struct{}{}
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// ApplyPatchIdempotent imports a quarantine delta into the authoritative
// workspace. A reverse-applicable patch is treated as already imported, which
// makes recovery safe after a crash between application and durable cleanup.
func (r Repo) ApplyPatchIdempotent(patch []byte, finalPermissions FilePermissions) (bool, error) {
	applied := false
	if len(bytes.TrimSpace(patch)) > 0 {
		if _, err := r.run(patch, "apply", "--check", "--binary", "--whitespace=nowarn", "-"); err == nil {
			if _, err := r.run(patch, "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
				return false, fmt.Errorf("import actor quarantine changes: %w", err)
			}
			applied = true
		} else if _, err := r.run(patch, "apply", "--reverse", "--check", "--binary", "--whitespace=nowarn", "-"); err != nil {
			return false, fmt.Errorf("actor quarantine changes do not apply cleanly to the authoritative workspace")
		}
	}
	if err := applyFilePermissions(r.Root, finalPermissions); err != nil {
		return false, fmt.Errorf("restore actor quarantine permissions: %w", err)
	}
	return applied, nil
}

// AdoptActorHead advances the authoritative branch to an already validated
// actor commit while retaining the imported working-tree content. A mixed
// reset updates HEAD and the index but never discards working-tree files.
func (r Repo) AdoptActorHead(commit string) error {
	if commit == "" {
		return fmt.Errorf("actor commit is empty")
	}
	_, err := r.run(nil, "reset", "--mixed", commit)
	return err
}

// Remove unregisters and deletes a compliant actor worktree. Safety failures
// deliberately do not call Remove so operators can inspect the checkout.
func (w *ActorWorktree) Remove() error {
	if w == nil {
		return nil
	}
	if err := w.validateCleanupPaths(); err != nil {
		return fmt.Errorf("validate actor quarantine cleanup: %w", err)
	}
	pins, err := w.baselinePins()
	if err != nil {
		return fmt.Errorf("resolve actor quarantine baseline pins: %w", err)
	}
	for _, pin := range pins {
		if err := validateActorBaselinePin(pin); err != nil {
			return fmt.Errorf("validate actor quarantine cleanup: %w", err)
		}
	}
	rootPath := filepath.Dir(w.Parent)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open actor quarantine root: %w", err)
	}
	defer root.Close()
	if _, err := w.Primary.run(nil, "worktree", "remove", "--force", w.Repo.Root); err != nil {
		return fmt.Errorf("remove actor quarantine worktree: %w", err)
	}
	if w.BaselineTree != "" {
		ref, err := actorBaselineRef(w.Repo.Root, "")
		if err != nil {
			return fmt.Errorf("identify actor quarantine baseline pin: %w", err)
		}
		if err := releaseActorBaselinePin(actorBaselinePin{
			repo: w.Primary,
			ref:  ref,
			tree: w.BaselineTree,
		}, false); err != nil {
			return err
		}
	}
	if err := root.Remove(filepath.Base(w.Parent)); err != nil {
		return fmt.Errorf("remove actor quarantine directory: %w", err)
	}
	return nil
}
