package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const workflowDirectory = ".agent-workflows"

// DiscoveryFile is a workflow definition found in one of the discovery
// scopes. Name is the filename without its .yaml or .yml suffix. Source is
// either repository or global.
type DiscoveryFile struct {
	Name   string
	Path   string
	Source string
}

// Discovery contains the deterministic workflow files available to a
// selector. Files are local-first after global shadowing has been applied;
// Names is the sorted logical-name list used by diagnostics and selection.
type Discovery struct {
	Files []DiscoveryFile
	Names []string
}

// DiscoverFiles finds workflow definitions in the repository and home
// discovery scopes. A missing discovery directory is treated as empty. The
// homeDir function is injectable so callers and tests do not depend on the
// process user's home directory.
func DiscoverFiles(repoRoot string, homeDir func() (string, error)) (Discovery, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return Discovery{}, fmt.Errorf("resolve repository discovery root: %w", err)
	}
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	homeRoot, err := homeDir()
	if err != nil {
		return Discovery{}, fmt.Errorf("resolve home directory: %w", err)
	}
	homeRoot, err = filepath.Abs(homeRoot)
	if err != nil {
		return Discovery{}, fmt.Errorf("resolve home discovery root: %w", err)
	}

	localDir := filepath.Join(repoRoot, workflowDirectory)
	globalDir := filepath.Join(homeRoot, workflowDirectory)
	local, err := discoverScope(localDir, "repository")
	if err != nil {
		return Discovery{}, err
	}
	global, err := discoverScope(globalDir, "global")
	if err != nil {
		return Discovery{}, err
	}

	filesByName := make(map[string]DiscoveryFile, len(local)+len(global))
	for name, file := range local {
		filesByName[name] = file
	}
	for name, file := range global {
		if _, shadowed := filesByName[name]; !shadowed {
			filesByName[name] = file
		}
	}

	names := make([]string, 0, len(filesByName))
	files := make([]DiscoveryFile, 0, len(filesByName))
	for name, file := range filesByName {
		names = append(names, name)
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	sort.Strings(names)
	return Discovery{Files: files, Names: names}, nil
}

// ResolveFile resolves a simple logical workflow selector using repository
// precedence over the home discovery scope.
func ResolveFile(repoRoot, selector string, homeDir func() (string, error)) (string, error) {
	if err := ValidateSelector(selector); err != nil {
		return "", err
	}
	discovery, err := DiscoverFiles(repoRoot, homeDir)
	if err != nil {
		return "", err
	}
	for _, file := range discovery.Files {
		if file.Name == selector {
			return file.Path, nil
		}
	}

	repoRoot, err = filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository discovery root: %w", err)
	}
	return "", fmt.Errorf(
		"unknown workflow selector %q; searched %s and ~/.agent-workflows (available: %s)",
		selector,
		filepath.Join(repoRoot, workflowDirectory),
		availableNames(discovery.Names),
	)
}

// ValidateSelector rejects path-like values so positional arguments cannot
// silently become an alternative workflow-file syntax.
func ValidateSelector(selector string) error {
	if selector == "" || selector == "." || selector == ".." || strings.ContainsAny(selector, `/\\`) {
		return fmt.Errorf("workflow selector %q must be a simple basename without path separators", selector)
	}
	return nil
}

func discoverScope(directory, source string) (map[string]DiscoveryFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]DiscoveryFile{}, nil
		}
		return nil, fmt.Errorf("discover workflows in %s: %w", directory, err)
	}

	files := make(map[string]DiscoveryFile)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect workflow candidate %s: %w", filepath.Join(directory, entry.Name()), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		name, ok := logicalName(entry.Name())
		if !ok {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if existing, duplicate := files[name]; duplicate {
			return nil, fmt.Errorf("duplicate workflow selector %q in %s: %s and %s", name, directory, existing.Path, path)
		}
		files[name] = DiscoveryFile{Name: name, Path: path, Source: source}
	}
	return files, nil
}

func logicalName(filename string) (string, bool) {
	for _, extension := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(filename, extension) {
			name := strings.TrimSuffix(filename, extension)
			return name, name != ""
		}
	}
	return "", false
}

func availableNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
