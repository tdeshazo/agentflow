package engine

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/provider"
)

func TestSemanticCompilerHasNoProviderDependency(t *testing.T) {
	for _, filename := range []string{"context_compiler.go", "semantic_context.go"} {
		path := filepath.Join(".", filename)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		for _, imported := range file.Imports {
			if strings.Contains(imported.Path.Value, "agentflow/provider") {
				t.Fatalf("semantic compiler %s imports provider", filename)
			}
		}
	}

	file, err := parser.ParseFile(token.NewFileSet(), "context_compiler.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var usesAny bool
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == "any" {
			usesAny = true
		}
		return true
	})
	if usesAny {
		t.Fatal("semantic compiler must not use any for provider-derived values")
	}
	payload, ok := reflect.TypeOf(AcceptedHandoff{}).FieldByName("Payload")
	if !ok || payload.Type != reflect.TypeOf(semanticHandoff{}) {
		t.Fatalf("accepted handoff payload must be a semantic value, got %v", payload.Type)
	}
}

func TestProviderHandoffProjectionIsRestrictedToInvocationContextAdapter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier, ok := call.Fun.(*ast.Ident)
			if ok && identifier.Name == "projectHandoff" && filename != "provider_projection.go" {
				t.Errorf("provider handoff projection called from %s", filename)
			}
			return true
		})
	}
}

func TestProviderProjectionDoesNotChangeCompiledSemantics(t *testing.T) {
	semantic := semanticInvocationContext{
		Encoding:     semanticContextEncoding,
		Objective:    "preserve authoritative state",
		Dependencies: []semanticDependencyContext{{Phase: "producer", Commit: "abc"}},
		Artifacts:    []semanticArtifactReference{{Name: "result", Producer: "producer", Path: semanticWorkspacePlaceholder + "/result.txt"}},
		Evidence:     []semanticEvidenceReference{},
		Handoffs: []semanticHandoffReference{{
			Producer: "producer",
			Commit:   "abc",
			Digest:   "sha256:handoff",
			Payload: semanticHandoff{
				Encoding:    semanticHandoffEncoding,
				Status:      "complete",
				Summary:     "advisory",
				Changes:     []semanticHandoffChange{},
				Findings:    []semanticHandoffFinding{},
				Checks:      []string{},
				Risks:       []string{},
				Blockers:    []string{},
				NextActions: []string{},
			},
		}},
		Validations: []semanticValidationRequirement{},
	}
	compiled, err := (&Engine{}).compileFreshInvocationContext(semantic)
	if err != nil {
		t.Fatal(err)
	}
	before, err := canonicalContextBytes(compiled)
	if err != nil {
		t.Fatal(err)
	}

	first := projectInvocationContext(compiled)
	second := projectInvocationContext(compiled)
	if first.Version != provider.InvocationContextVersionV2 || !reflect.DeepEqual(first, second) {
		t.Fatalf("projection is not deterministic: first=%#v second=%#v", first, second)
	}
	first.Dependencies[0].Commit = "projection-only-change"
	first.Receipt.Selected = append(first.Receipt.Selected, "projection-only-change")

	after, err := canonicalContextBytes(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("provider projection mutated semantic selection or digest inputs")
	}
	projectedBytes, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectedBytes) > invocationContextCeiling {
		t.Fatalf("projection bytes=%d, semantic receipt=%#v", len(projectedBytes), compiled.Receipt)
	}
}
