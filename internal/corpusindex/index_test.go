package corpusindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodingAgentWorkIndex(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRepository(root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIndexRejectsMalformedData(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	index := loadIndex(t, root)

	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "unsupported schema version",
			mutate: func(index map[string]any) {
				index["schema_version"] = "agentflow.dev/coding-agent-work-index/v999"
			},
			want: "schema_version",
		},
		{
			name: "duplicate stable ID",
			mutate: func(index map[string]any) {
				entries := index["entries"].([]any)
				entries[1].(map[string]any)["id"] = entries[0].(map[string]any)["id"]
			},
			want: "duplicate id",
		},
		{
			name: "missing required field",
			mutate: func(index map[string]any) {
				delete(index["entries"].([]any)[0].(map[string]any), "title")
			},
			want: "missing required field",
		},
		{
			name: "unsupported metric",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["metrics"].([]any)[0].(map[string]any)["name"] = "cost"
			},
			want: "expected",
		},
		{
			name: "unsupported metric evidence",
			mutate: func(index map[string]any) {
				provenance := metricAt(index, 0, 0)["provenance"].(map[string]any)
				provenance["evidence_ids"].([]any)[0] = "invented-evidence"
			},
			want: "unknown evidence",
		},
		{
			name: "derived metric missing calculation",
			mutate: func(index map[string]any) {
				delete(metricAt(index, 0, 2)["provenance"].(map[string]any), "calculation")
			},
			want: "missing required field",
		},
		{
			name: "derived calculation does not reproduce value",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 2)["value"] = json.Number("1")
			},
			want: "does not match",
		},
		{
			name: "review metric rejects sum calculation",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 2)["provenance"].(map[string]any)["calculation"] = map[string]any{
					"operation": "sum",
					"operands":  []any{json.Number("1")},
				}
			},
			want: "missing required field",
		},
		{
			name: "exact metric missing measurement",
			mutate: func(index map[string]any) {
				metric := metricAt(index, 0, 0)
				metric["status"] = "exact"
				metric["value"] = json.Number("1")
			},
			want: "missing required field",
		},
		{
			name: "unavailable metric with extraneous calculation",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 0)["provenance"].(map[string]any)["calculation"] = "unsupported assertion"
			},
			want: "unsupported field",
		},
		{
			name: "status value mismatch",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 0)["value"] = json.Number("1")
			},
			want: "must not include value",
		},
		{
			name: "fractional token count",
			mutate: func(index map[string]any) {
				metric := metricAt(index, 0, 0)
				metric["status"] = "derived"
				metric["value"] = json.Number("1.5")
				metric["provenance"] = map[string]any{
					"evidence_ids": []any{"implementation-trace"},
					"calculation":  "A reproducible but fractional test calculation.",
				}
			},
			want: "non-negative integer",
		},
		{
			name: "derived count overflow",
			mutate: func(index map[string]any) {
				metric := metricAt(index, 0, 0)
				metric["status"] = "derived"
				metric["value"] = json.Number("0")
				metric["provenance"] = map[string]any{
					"evidence_ids": []any{"implementation-trace"},
					"calculation": map[string]any{
						"operation": "sum",
						"operands":  []any{json.Number("18446744073709551615"), json.Number("1")},
					},
				}
			},
			want: "overflows uint64",
		},
		{
			name: "negative review minutes",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 2)["value"] = json.Number("-1")
			},
			want: "non-negative",
		},
		{
			name: "wrong metric unit",
			mutate: func(index map[string]any) {
				metricAt(index, 0, 1)["unit"] = "tokens"
			},
			want: "want",
		},
		{
			name: "empty trace actions",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["trace"].(map[string]any)["actions"] = []any{}
			},
			want: "non-empty",
		},
		{
			name: "trace extraneous field",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["trace"].(map[string]any)["provider"] = "forbidden"
			},
			want: "unsupported field",
		},
		{
			name: "unsafe field",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["raw_prompt"] = "forbidden"
			},
			want: "unsafe field",
		},
		{
			name: "normalized unsafe field",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["Raw-Prompt"] = "forbidden"
			},
			want: "unsafe field",
		},
		{
			name: "sensitive bearer value",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["title"] = "Bearer abcdefghijklmnopqrstuvwxyz012345"
			},
			want: "sensitive value",
		},
		{
			name: "private key material",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["scope"] = "-----BEGIN PRIVATE KEY-----"
			},
			want: "sensitive value",
		},
		{
			name: "broken stable reference",
			mutate: func(index map[string]any) {
				index["entries"].([]any)[0].(map[string]any)["evidence"].([]any)[0].(map[string]any)["repo_relative_path"] = "missing.txt"
			},
			want: "does not resolve",
		},
		{
			name: "non deterministic ordering",
			mutate: func(index map[string]any) {
				entries := index["entries"].([]any)
				entries[0], entries[1] = entries[1], entries[0]
			},
			want: "lexicographically ordered",
		},
		{
			name: "unknown observed task",
			mutate: func(index map[string]any) {
				index["failure_modes"].([]any)[0].(map[string]any)["observed_task_ids"].([]any)[0] = "invented-task"
			},
			want: "unknown task",
		},
		{
			name: "frequency count mismatch",
			mutate: func(index map[string]any) {
				index["failure_modes"].([]any)[0].(map[string]any)["frequency"].(map[string]any)["value"] = json.Number("2")
			},
			want: "must equal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneIndex(t, index)
			test.mutate(candidate)
			err := validateIndex(root, candidate)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateIndex() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestDerivedCountCalculationPreservesIntegersAboveTwoToTheFiftyThird(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	index := loadIndex(t, root)
	metric := metricAt(index, 0, 0)
	metric["status"] = "derived"
	metric["value"] = json.Number("9007199254740993")
	metric["provenance"] = map[string]any{
		"evidence_ids": []any{"implementation-trace"},
		"calculation": map[string]any{
			"operation": "sum",
			"operands":  []any{json.Number("9007199254740992"), json.Number("1")},
		},
	}
	if err := validateIndex(root, index); err != nil {
		t.Fatalf("validateIndex() error = %v, want exact integer sum accepted", err)
	}
}

func TestSchemaMatchesValidatorContract(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	schema := loadSchema(t, root)
	if err := validateSchemaDocument(schema); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "task class enum drift",
			mutate: func(schema map[string]any) {
				taskClass := schemaDefinitionProperty(schema, "entry", "task_class")
				taskClass["enum"] = taskClass["enum"].([]any)[:5]
			},
		},
		{
			name: "metric order drift",
			mutate: func(schema map[string]any) {
				metrics := schemaDefinitionProperty(schema, "entry", "metrics")
				metrics["prefixItems"].([]any)[0].(map[string]any)["$ref"] = "#/$defs/reviewMetric"
			},
		},
		{
			name: "metric cardinality drift",
			mutate: func(schema map[string]any) {
				schemaDefinitionProperty(schema, "entry", "metrics")["minItems"] = float64(2)
			},
		},
		{
			name: "metric conditional drift",
			mutate: func(schema map[string]any) {
				metric := schema["$defs"].(map[string]any)["metric"].(map[string]any)
				metric["allOf"] = metric["allOf"].([]any)[:3]
			},
		},
		{
			name: "trace conditional body drift",
			mutate: func(schema map[string]any) {
				trace := schema["$defs"].(map[string]any)["trace"].(map[string]any)
				delete(trace["allOf"].([]any)[0].(map[string]any), "then")
			},
		},
		{
			name: "token calculation operation drift",
			mutate: func(schema map[string]any) {
				metric := schema["$defs"].(map[string]any)["tokenMetric"].(map[string]any)
				then := metric["allOf"].([]any)[2].(map[string]any)["then"].(map[string]any)
				properties := then["properties"].(map[string]any)
				provenance := properties["provenance"].(map[string]any)
				calculation := provenance["properties"].(map[string]any)["calculation"].(map[string]any)
				calculation["$ref"] = "#/$defs/elapsedMinutesCalculation"
			},
		},
		{
			name: "count maximum drift",
			mutate: func(schema map[string]any) {
				metric := schema["$defs"].(map[string]any)["toolMetric"].(map[string]any)
				constraint := metric["allOf"].([]any)[1].(map[string]any)
				value := constraint["properties"].(map[string]any)["value"].(map[string]any)
				delete(value, "maximum")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneSchema(t, schema)
			test.mutate(candidate)
			if err := validateSchemaDocument(candidate); err == nil {
				t.Fatal("validateSchemaDocument() unexpectedly succeeded")
			}
		})
	}
}

func TestValidateRepositoryPathRejectsIntermediateSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "proof.txt"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "evidence")); err != nil {
		t.Fatal(err)
	}
	err := validateRepositoryPath(root, "evidence/proof.txt")
	if err == nil || !strings.Contains(err.Error(), "does not resolve within repository") {
		t.Fatalf("validateRepositoryPath() error = %v, want confined-resolution error", err)
	}
}

func loadIndex(t *testing.T, root string) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, indexPath))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	var index map[string]any
	if err := decoder.Decode(&index); err != nil {
		t.Fatal(err)
	}
	return index
}

func cloneIndex(t *testing.T, index map[string]any) map[string]any {
	t.Helper()
	contents, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	var clone map[string]any
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func metricAt(index map[string]any, entry, metric int) map[string]any {
	return index["entries"].([]any)[entry].(map[string]any)["metrics"].([]any)[metric].(map[string]any)
}

func loadSchema(t *testing.T, root string) map[string]any {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, schemaPath))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func cloneSchema(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	contents, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	var clone map[string]any
	if err := decoder.Decode(&clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func schemaDefinitionProperty(schema map[string]any, definition, property string) map[string]any {
	return schema["$defs"].(map[string]any)[definition].(map[string]any)["properties"].(map[string]any)[property].(map[string]any)
}
