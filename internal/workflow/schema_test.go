package workflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGeneratedSchemaArtifactsAreCurrent(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate schema test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	for _, version := range []string{v1alpha1APIVersion, v1alpha2APIVersion} {
		t.Run(version, func(t *testing.T) {
			want, err := GeneratedSchema(version)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "schema", strings.TrimPrefix(version, "agentflow.dev/")+".schema.json")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, append(want, '\n')) {
				t.Fatalf("%s is stale; run go generate ./internal/workflow", path)
			}
		})
	}
}

func TestGeneratedSchemaDeclaresAuthorityAndRuntimeBoundary(t *testing.T) {
	contents, err := GeneratedSchema(v1alpha1APIVersion)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema["x-agentflow-validation"].(map[string]any)["unknown-fields"]; got != "rejected" {
		t.Fatalf("unknown fields = %#v, want rejected", got)
	}
	definitions := schema["$defs"].(map[string]any)
	if got := definitions["Metadata"].(map[string]any)["x-agentflow-field-classification"]; got != "descriptive" {
		t.Fatalf("metadata classification = %#v", got)
	}
	if got := definitions["Spec"].(map[string]any)["x-agentflow-field-classification"]; got != "executable" {
		t.Fatalf("spec classification = %#v", got)
	}
	properties := definitions["IntegrityRule"].(map[string]any)["properties"].(map[string]any)
	if got := properties["allowed_semantic_changes"].(map[string]any)["x-agentflow-runtime-status"]; got != "unsupported" {
		t.Fatalf("allowed_semantic_changes status = %#v", got)
	}
	workspace := definitions["WorkspaceSpec"].(map[string]any)["properties"].(map[string]any)
	if _, ok := workspace["allowWrites"]; !ok {
		t.Fatal("v1alpha1 schema omitted concise workspace.allowWrites")
	}
	validation := definitions["Validation"].(map[string]any)["properties"].(map[string]any)
	if _, ok := validation["run"]; !ok {
		t.Fatal("v1alpha1 schema omitted concise validation.run")
	}
	phase := definitions["Phase"].(map[string]any)["properties"].(map[string]any)
	if _, ok := phase["actor"].(map[string]any)["oneOf"]; !ok {
		t.Fatal("v1alpha1 schema omitted mapping-valued phase.actor")
	}
}

func TestReferenceDocumentsPointToGeneratedSchemas(t *testing.T) {
	for _, path := range []string{
		filepath.Join("..", "..", "docs", "reference", "agentflow-v1alpha1.md"),
		filepath.Join("..", "..", "docs", "reference", "agentflow-v1alpha2.md"),
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(contents), "schema/") || !strings.Contains(string(contents), "GeneratedSchema") {
				t.Fatalf("%s must identify the checked-in generated schema authority", path)
			}
		})
	}
}
