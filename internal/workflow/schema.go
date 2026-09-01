package workflow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

//go:generate go run ./cmd/schemagen -out ../../schema

// GeneratedSchema returns the checked-in JSON Schema contract for an
// authoring API version. The structural contract comes from the same Go types
// that Decode uses with KnownFields. Validate remains responsible for
// cross-reference, expression, and runtime-surface checks.
func GeneratedSchema(apiVersion string) ([]byte, error) {
	var root reflect.Type
	switch apiVersion {
	case v1alpha1APIVersion:
		root = reflect.TypeFor[Workflow]()
	case v1alpha2APIVersion:
		root = reflect.TypeFor[V1Alpha2Workflow]()
	case v1alpha3APIVersion:
		root = reflect.TypeFor[V1Alpha3Workflow]()
	case v1alpha4APIVersion:
		root = reflect.TypeFor[V1Alpha4Workflow]()
	default:
		return nil, fmt.Errorf("unsupported workflow schema version %q", apiVersion)
	}

	generator := schemaGenerator{definitions: map[string]any{}}
	body := generator.object(root)
	properties := body["properties"].(map[string]any)
	properties["apiVersion"] = map[string]any{"const": apiVersion}
	properties["kind"] = map[string]any{"const": "AgentWorkflow"}
	body["required"] = []string{"apiVersion", "kind", "metadata", "spec"}
	body["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	body["$id"] = "https://agentflow.dev/schema/" + strings.TrimPrefix(apiVersion, "agentflow.dev/") + ".json"
	body["title"] = "AgentFlow " + strings.TrimPrefix(apiVersion, "agentflow.dev/") + " executable authoring contract"
	body["description"] = "Generated from the strict Go decoder. Run agentflow validate for source-aware semantic and runtime-support diagnostics."
	body["x-agentflow-validation"] = map[string]any{
		"strict-decoder":        "workflow.Decode",
		"semantic-validator":    "workflow.Validate",
		"unknown-fields":        "rejected",
		"unsupported-construct": "reported before execution",
	}
	body["$defs"] = generator.definitions
	return json.MarshalIndent(body, "", "  ")
}

type schemaGenerator struct {
	definitions map[string]any
	building    map[reflect.Type]bool
}

func (g *schemaGenerator) schema(t reflect.Type) any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		name := t.Name()
		if name == "" {
			return g.object(t)
		}
		if _, ok := g.definitions[name]; !ok && !g.building[t] {
			if g.building == nil {
				g.building = map[reflect.Type]bool{}
			}
			g.building[t] = true
			g.definitions[name] = g.object(t)
			delete(g.building, t)
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Interface:
		return true // Parameter.default accepts YAML values whose type Validate checks.
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return true
	}
}

func (g *schemaGenerator) object(t reflect.Type) map[string]any {
	properties := map[string]any{}
	for i := range t.NumField() {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		properties[tag] = g.field(t, field)
	}
	// v1alpha1 supports these concise spellings through the same strict decode
	// boundary after AST lowering. They are part of the authored surface even
	// though the normalized runtime model stores their expanded forms.
	switch t.Name() {
	case "WorkspaceSpec":
		properties["allowWrites"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case "Validation":
		properties["run"] = map[string]any{"type": "string"}
	}
	result := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	switch t.Name() {
	case "Metadata", "V1Alpha2Metadata", "Source":
		result["x-agentflow-field-classification"] = "descriptive"
	case "Spec", "V1Alpha2Spec", "V1Alpha3Spec", "V1Alpha4Spec":
		result["x-agentflow-field-classification"] = "executable"
	}
	return result
}

func (g *schemaGenerator) field(owner reflect.Type, field reflect.StructField) any {
	// These forms have intentionally strict custom YAML decoders. They remain
	// unions in the public schema while Decode is the final authority.
	switch field.Name {
	case "AssertProgress":
		return map[string]any{"oneOf": []any{map[string]any{"type": "boolean"}, g.schema(reflect.TypeFor[ProgressAssertion]())}}
	}
	if owner.Name() == "Phase" && field.Name == "Actor" {
		return map[string]any{"oneOf": []any{map[string]any{"type": "string"}, g.schema(reflect.TypeFor[Agent]())}}
	}
	if field.Type == reflect.TypeFor[[]ChecklistItem]() {
		return map[string]any{"type": "array", "items": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, g.schema(reflect.TypeFor[ChecklistItem]())}}}
	}
	if field.Type == reflect.TypeFor[CompletionStep]() {
		return map[string]any{"oneOf": []any{map[string]any{"type": "string"}, g.schema(field.Type)}}
	}
	if field.Type == reflect.TypeFor[V1Alpha2Actor]() {
		return map[string]any{"oneOf": []any{map[string]any{"type": "string"}, g.schema(reflect.TypeFor[V1Alpha2Agent]())}}
	}
	schema := g.schema(field.Type)
	if field.Name == "ID" {
		return map[string]any{"type": "string", "pattern": identifierPatternSource}
	}
	if namedMapField(owner.Name(), field.Name) {
		objectSchema := schema.(map[string]any)
		objectSchema["propertyNames"] = map[string]any{"pattern": identifierPatternSource}
		return objectSchema
	}
	if owner.Name() == "Recovery" && field.Name == "ActivePhase" {
		return map[string]any{
			"allOf":                      []any{schema},
			"x-agentflow-runtime-status": "unsupported",
			"description":                activePhaseRecoveryUnsupportedReason,
		}
	}
	if field.Name == "AllowedSemanticChanges" {
		annotated := map[string]any{"allOf": []any{schema}, "x-agentflow-runtime-status": "unsupported", "description": allowedSemanticChangesUnsupportedReason}
		return annotated
	}
	return schema
}

func namedMapField(owner, field string) bool {
	switch owner {
	case "Spec":
		switch field {
		case "Parameters", "Paths", "Agents", "Tools", "Validation", "Completion":
			return true
		}
	case "V1Alpha2Spec":
		return field == "Agents" || field == "Validation"
	case "V1Alpha3Spec":
		return field == "Agents" || field == "Validation" || field == "Artifacts" || field == "Evidence"
	case "V1Alpha4Spec":
		return field == "Agents" || field == "Validation" || field == "Artifacts" || field == "Evidence"
	case "StateRecords":
		return field == "Integrity"
	}
	return false
}

const allowedSemanticChangesUnsupportedReason = "allowed_semantic_changes is retained for source compatibility but is not enforced by this runtime; its use is reported as unsupported before execution"

const activePhaseRecoveryUnsupportedReason = "recovery.activePhase is retained for v1alpha1 source compatibility but the runtime derives recovery from durable state; its use is reported as unsupported before execution"
