package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFilesUsesDeterministicScopesAndNames(t *testing.T) {
	repoRoot := t.TempDir()
	homeRoot := t.TempDir()
	localDir := filepath.Join(repoRoot, workflowDirectory)
	globalDir := filepath.Join(homeRoot, workflowDirectory)
	for _, directory := range []string{localDir, globalDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeDiscoveryFile(t, filepath.Join(localDir, "local-only.yaml"))
	writeDiscoveryFile(t, filepath.Join(localDir, "shadowed.yml"))
	writeDiscoveryFile(t, filepath.Join(globalDir, "global-only.yml"))
	writeDiscoveryFile(t, filepath.Join(globalDir, "shadowed.yaml"))
	writeDiscoveryFile(t, filepath.Join(localDir, "ignored.txt"))
	if err := os.Mkdir(filepath.Join(localDir, "directory.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}

	discovery, err := DiscoverFiles(repoRoot, func() (string, error) { return homeRoot, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := discovery.Names, []string{"global-only", "local-only", "shadowed"}; !sameStrings(got, want) {
		t.Fatalf("discovered names = %#v, want %#v", got, want)
	}
	if got, want := discovery.Files[2].Path, filepath.Join(localDir, "shadowed.yml"); got != want {
		t.Fatalf("shadowed workflow path = %q, want %q", got, want)
	}
	if got, want := discovery.Files[0].Source, "global"; got != want {
		t.Fatalf("global workflow source = %q, want %q", got, want)
	}
	if got, want := discovery.Files[1].Source, "repository"; got != want {
		t.Fatalf("local workflow source = %q, want %q", got, want)
	}
	if got, want := discovery.Files[2].Source, "repository"; got != want {
		t.Fatalf("shadowed workflow source = %q, want %q", got, want)
	}
}

func TestDiscoverFilesMissingDirectoriesAreNormal(t *testing.T) {
	discovery, err := DiscoverFiles(t.TempDir(), func() (string, error) { return t.TempDir(), nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Files) != 0 || len(discovery.Names) != 0 {
		t.Fatalf("missing-directory discovery = %#v", discovery)
	}
}

func TestDiscoverFilesIgnoresLegacyDirectories(t *testing.T) {
	repoRoot := t.TempDir()
	homeRoot := t.TempDir()
	for _, directory := range []string{
		filepath.Join(repoRoot, ".agent-workflows"),
		filepath.Join(homeRoot, ".agent-workflows"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeDiscoveryFile(t, filepath.Join(repoRoot, ".agent-workflows", "legacy-local.yaml"))
	writeDiscoveryFile(t, filepath.Join(homeRoot, ".agent-workflows", "legacy-global.yaml"))

	discovery, err := DiscoverFiles(repoRoot, func() (string, error) { return homeRoot, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Files) != 0 {
		t.Fatalf("legacy workflows were discovered: %#v", discovery.Files)
	}
}

func TestDiscoverFilesRejectsDuplicateLogicalNames(t *testing.T) {
	tests := []struct {
		name       string
		duplicate  string
		otherRoot  func(*testing.T) string
		expectedIn string
	}{
		{
			name:       "repository scope",
			duplicate:  "repo",
			otherRoot:  func(t *testing.T) string { return t.TempDir() },
			expectedIn: filepath.FromSlash(".agentflow/workflows"),
		},
		{
			name:       "home scope",
			duplicate:  "home",
			otherRoot:  func(t *testing.T) string { return t.TempDir() },
			expectedIn: filepath.FromSlash(".agentflow/workflows"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			homeRoot := tt.otherRoot(t)
			root := repoRoot
			if tt.name == "home scope" {
				root = homeRoot
			}
			directory := filepath.Join(root, workflowDirectory)
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			writeDiscoveryFile(t, filepath.Join(directory, tt.duplicate+".yaml"))
			writeDiscoveryFile(t, filepath.Join(directory, tt.duplicate+".yml"))

			_, err := DiscoverFiles(repoRoot, func() (string, error) { return homeRoot, nil })
			if err == nil || !strings.Contains(err.Error(), `duplicate workflow selector "`+tt.duplicate+`"`) || !strings.Contains(err.Error(), tt.expectedIn) {
				t.Fatalf("duplicate error = %v", err)
			}
		})
	}
}

func TestResolveFileUnknownSelectorNamesLocationsWithoutHomeDetails(t *testing.T) {
	repoRoot := t.TempDir()
	homeRoot := filepath.Join(t.TempDir(), "private-home")
	errText := ""
	_, err := ResolveFile(repoRoot, "missing", func() (string, error) { return homeRoot, nil })
	if err != nil {
		errText = err.Error()
	}
	if !strings.Contains(errText, `unknown workflow selector "missing"`) ||
		!strings.Contains(errText, filepath.Join(repoRoot, workflowDirectory)) ||
		!strings.Contains(errText, "~/.agentflow/workflows") ||
		strings.Contains(errText, "private-home") {
		t.Fatalf("unknown selector error = %q", errText)
	}
}

func TestValidateSelectorRejectsPaths(t *testing.T) {
	for _, selector := range []string{"", ".", "..", "nested/workflow", `nested\workflow`} {
		if err := ValidateSelector(selector); err == nil || !strings.Contains(err.Error(), "simple basename") {
			t.Fatalf("selector %q error = %v", selector, err)
		}
	}
}

func writeDiscoveryFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("workflow: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
