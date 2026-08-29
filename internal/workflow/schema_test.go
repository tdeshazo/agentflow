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
	recovery := definitions["Recovery"].(map[string]any)["properties"].(map[string]any)
	if got := recovery["activePhase"].(map[string]any)["x-agentflow-runtime-status"]; got != "unsupported" {
		t.Fatalf("activePhase recovery status = %#v", got)
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
	if got := phase["id"].(map[string]any)["pattern"]; got != identifierPatternSource {
		t.Fatalf("phase identifier pattern = %#v", got)
	}
	specProperties := definitions["Spec"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"parameters", "paths", "agents", "tools", "validation", "completion"} {
		named := specProperties[name].(map[string]any)
		if got := named["propertyNames"].(map[string]any)["pattern"]; got != identifierPatternSource {
			t.Fatalf("named %s pattern = %#v", name, got)
		}
	}
	loop := definitions["Loop"].(map[string]any)["properties"].(map[string]any)["dispatchByCriterion"].(map[string]any)
	if _, restricted := loop["propertyNames"]; restricted {
		t.Fatal("legacy display-text dispatch keys must remain schema-compatible")
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

func TestPublicAuthoringContractCoversBothExecutableVersions(t *testing.T) {
	cases := []struct {
		path      string
		fragments []string
	}{
		{
			path: filepath.Join("..", "..", "README.md"),
			fragments: []string{
				"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2",
				"every checked-in executable workflow definition under `spec/` and `examples/`",
				"does not make live model calls",
			},
		},
		{
			path: filepath.Join("..", "..", "docs", "guides", "development.md"),
			fragments: []string{
				"every checked-in executable workflow definition under `spec/` and `examples/`",
				"examples/feature.agent-workflow.yaml", "plan --expanded",
			},
		},
		{
			path: filepath.Join("..", "..", "skills", "agentflow-spec", "SKILL.md"),
			fragments: []string{
				"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2",
				"../../docs/reference/agentflow-v1alpha2.md", "normalizedExecution",
			},
		},
		{
			path: filepath.Join("..", "..", "scripts", "check.sh"),
			fragments: []string{
				"rg --files spec examples", "validate -f \"$workflow\"",
			},
		},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			contents, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(string(contents), fragment) {
					t.Fatalf("%s must contain %q", tc.path, fragment)
				}
			}
		})
	}
}
