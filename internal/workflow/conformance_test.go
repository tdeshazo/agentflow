package workflow

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConformanceCorpus(t *testing.T) {
	cases := []struct {
		name   string
		status Status
	}{
		{name: "valid/minimal.yaml", status: Executable},
		{name: "valid/concise-defaults.yaml", status: Executable},
		{name: "unsupported/runtime-surface.yaml", status: Unsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
		})
	}
}

func TestConformanceInvalidDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		status   Status
		contains string
	}{
		{name: "unknown-executable-field.yaml", status: Invalid, contains: "field unknowable not found"},
		{name: "malformed-type.yaml", status: Invalid, contains: "cannot unmarshal"},
		{name: "duplicate-identifiers.yaml", path: "spec.preconditions[1].id", status: Invalid, contains: "duplicate check id"},
		{name: "unknown-references.yaml", path: "spec.phases[0].actor", status: Invalid, contains: "unknown agent"},
		{name: "invalid-expression.yaml", path: "spec.workspace.root", status: Invalid, contains: "unknown parameter reference"},
		{name: "malformed-expression.yaml", path: "spec.state.reset.when", status: Invalid, contains: "missing closing delimiter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", "invalid", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if (tc.path == "" || diagnostic.Path == tc.path) &&
					diagnostic.Status == tc.status && strings.Contains(diagnostic.Message, tc.contains) {
					return
				}
			}
			t.Fatalf("no diagnostic with path %q and message containing %q: %#v", tc.path, tc.contains, result.Diagnostics)
		})
	}
}

func TestConformanceDiagnosticOrderIsStable(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "invalid", "unknown-references.yaml")
	first := ValidateFile(path)
	for i := 0; i < 20; i++ {
		result := ValidateFile(path)
		if len(result.Diagnostics) != len(first.Diagnostics) {
			t.Fatalf("run %d diagnostics = %d, want %d", i, len(result.Diagnostics), len(first.Diagnostics))
		}
		for j, diagnostic := range result.Diagnostics {
			want := first.Diagnostics[j]
			if diagnostic.Status != want.Status || diagnostic.Path != want.Path || diagnostic.Position != want.Position || diagnostic.Message != want.Message {
				t.Fatalf("run %d diagnostic %d changed:\n got %#v\nwant %#v", i, j, diagnostic, want)
			}
		}
	}
}

func TestConformanceShippedDefinitions(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"),
	}
	for _, path := range paths {
		result := ValidateFile(path)
		if result.Status == Invalid {
			t.Fatalf("%s invalid: %#v", path, result.Diagnostics)
		}
	}
}
