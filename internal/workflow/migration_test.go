package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV1Alpha1MigrationMatrixIsCompleteForCanonicalWorkflow(t *testing.T) {
	matrix, err := V1Alpha1MigrationMatrix()
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Status != "supported-maintenance-frozen" {
		t.Fatalf("matrix status = %q", matrix.Status)
	}

	report, err := MigrationCheckFile(filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) == 0 {
		t.Fatal("migration report has no diagnostics")
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Note == "matrix coverage missing" {
			t.Fatalf("unclassified supported field: %#v", diagnostic)
		}
	}
}

func TestV1Alpha1MigrationMatrixClassifiesEverySchemaField(t *testing.T) {
	matrix, err := V1Alpha1MigrationMatrix()
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0)
	collectWorkflowFieldPaths(reflect.TypeFor[Workflow](), "", &paths, map[reflect.Type]bool{})
	// These strict concise spellings are part of v1alpha1's authored surface
	// even though AST lowering stores their expanded form in Workflow.
	paths = append(paths, "spec.workspace.allowWrites[]", "spec.validation.*.run")
	for _, path := range paths {
		if _, ok := migrationCapabilityFor(path, matrix); !ok {
			t.Errorf("matrix does not classify supported field %s", path)
		}
	}
}

func collectWorkflowFieldPaths(t reflect.Type, path string, paths *[]string, stack map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if stack[t] {
			return
		}
		stack[t] = true
		defer delete(stack, t)
		for i := range t.NumField() {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if !field.IsExported() || tag == "" || tag == "-" {
				continue
			}
			child := tag
			if path != "" {
				child = path + "." + tag
			}
			collectWorkflowFieldPaths(field.Type, child, paths, stack)
		}
	case reflect.Map:
		collectWorkflowFieldPaths(t.Elem(), path+".*", paths, stack)
	case reflect.Array, reflect.Slice:
		collectWorkflowFieldPaths(t.Elem(), path+"[]", paths, stack)
	default:
		*paths = append(*paths, path)
	}
}

func TestMigrationCheckClassifiesRepresentativesWithoutRewriting(t *testing.T) {
	path := writeWorkflow(t, executableFixture)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := MigrationCheckFile(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("migration check rewrote its source")
	}
	got := map[string]MigrationClass{}
	for _, diagnostic := range report.Diagnostics {
		got[diagnostic.Path] = diagnostic.Classification
	}
	for path, want := range map[string]MigrationClass{
		"spec.agents.worker.runner": DirectSuccessorCapability,
		"spec.tools.scope.type":     GeneralizedReplacement,
		"spec.phases[0].prompt":     DirectSuccessorCapability,
		"spec.flow[0].phase":        GeneralizedReplacement,
	} {
		if got[path] != want {
			t.Errorf("%s classification = %q, want %q", path, got[path], want)
		}
	}
}

func TestMigrationCheckRejectsNonV1Alpha1Source(t *testing.T) {
	_, err := MigrationCheckFile(filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml"))
	if err == nil || !strings.Contains(err.Error(), "requires an agentflow.dev/v1alpha1") {
		t.Fatalf("error = %v", err)
	}
}
