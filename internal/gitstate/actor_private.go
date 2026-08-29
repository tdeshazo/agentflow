package gitstate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var actorPrivateRoots = []string{
	".agentflow",
	".agents/skills/agentflow-spec",
}

// IsActorPrivatePath reports whether path is inside a runtime control
// namespace that is private to the authoritative process.
func IsActorPrivatePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))))
	for _, root := range actorPrivateRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func rejectTrackedActorPrivatePaths(repo Repo) error {
	output, err := repo.run(nil, "ls-files", "-z", "--", ".")
	if err != nil {
		return err
	}
	private := []string{}
	for _, path := range strings.Split(string(output), "\x00") {
		if path != "" && IsActorPrivatePath(path) {
			private = append(private, filepath.ToSlash(filepath.Clean(path)))
		}
	}
	if len(private) == 0 {
		return nil
	}
	sort.Strings(private)
	return fmt.Errorf(
		"actor-private runtime control path is tracked by Git and would remain readable from the actor repository snapshot: %s",
		strings.Join(private, ", "),
	)
}

func actorPrivateAbsolutePaths(root string) []string {
	paths := make([]string, 0, len(actorPrivateRoots))
	for _, path := range actorPrivateRoots {
		paths = append(paths, filepath.Join(root, filepath.FromSlash(path)))
	}
	return paths
}

func removeActorPrivatePaths(root string) error {
	for _, path := range actorPrivateAbsolutePaths(root) {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove actor-private path %q: %w", path, err)
		}
	}
	return nil
}

func actorVisibleFilePermissions(permissions FilePermissions) FilePermissions {
	visible := make(FilePermissions, len(permissions))
	for path, mode := range permissions {
		if !IsActorPrivatePath(path) {
			visible[path] = mode
		}
	}
	return visible
}
