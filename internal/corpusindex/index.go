package corpusindex

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	indexPath     = "docs/evidence/coding-agent-work-index/v1.json"
	schemaPath    = "docs/evidence/coding-agent-work-index/v1.schema.json"
	schemaVersion = "agentflow.dev/coding-agent-work-index/v1"
	maxCount      = "18446744073709551615"
)

func set(values ...string) map[string]bool {
	r := map[string]bool{}
	for _, v := range values {
		r[v] = true
	}
	return r
}

var taskClasses = set("implementation", "review_correction", "isolated_workflow_validation", "runtime_safety", "semantic_handoff", "provider_tool_extensibility")
var outcomeStatuses = set("implementation_evidence_present", "review_findings_documented", "validation_evidence_present")
var evidenceKinds = set("evidence_document", "example", "implementation", "review_document", "sanitized_trace", "test")
var metricStatuses = set("exact", "derived", "unavailable", "not_applicable")
var traceStatuses = set("captured", "unavailable")
var frequencyStatuses = set("sample_observed", "unavailable")
var metricUnits = map[string]string{"token_count": "tokens", "tool_calls": "calls", "review_time": "minutes"}

var unsafeFields = set("credential", "credentials", "finalmessage", "pid", "privatereasoning", "processid", "prompt", "rawprompt", "rawtranscript", "reasoning", "secret", "secrets", "tooloutput", "transcript", "provideroutput")
var sensitiveValues = []*regexp.Regexp{
	regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}={0,2}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)\b(?:api[_-]?key|client[_-]?secret|access[_-]?token|auth[_-]?token)\s*[:=]\s*["']?[A-Za-z0-9._~+/-]{12,}`),
}

func validateRepository(root string) error {
	if err := validateSchema(root); err != nil {
		return err
	}
	b, err := readRepositoryFile(root, indexPath)
	if err != nil {
		return fmt.Errorf("read coding-agent work index: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	var index map[string]any
	if err := decoder.Decode(&index); err != nil {
		return fmt.Errorf("decode coding-agent work index: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return validateIndex(root, index)
}

func readRepositoryFile(root, path string) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func validateSchema(root string) error {
	b, err := readRepositoryFile(root, schemaPath)
	if err != nil {
		return fmt.Errorf("read coding-agent work index schema: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.UseNumber()
	var schema map[string]any
	if err := decoder.Decode(&schema); err != nil {
		return fmt.Errorf("decode coding-agent work index schema: %w", err)
	}
	return validateSchemaDocument(schema)
}

func validateSchemaDocument(schema map[string]any) error {
	if err := requireString(schema, "$schema", "https://json-schema.org/draft/2020-12/schema"); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if err := requireString(schema, "$id", schemaPath); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	properties, err := object(schema, "properties")
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	version, err := object(properties, "schema_version")
	if err != nil {
		return fmt.Errorf("schema: schema_version: %w", err)
	}
	if err := requireString(version, "const", schemaVersion); err != nil {
		return fmt.Errorf("schema: schema_version: %w", err)
	}
	defs, err := object(schema, "$defs")
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	checks := []struct {
		def, property string
		values        map[string]bool
	}{
		{"entry", "task_class", taskClasses}, {"outcome", "status", outcomeStatuses},
		{"evidence", "kind", evidenceKinds}, {"metric", "status", metricStatuses},
		{"trace", "status", traceStatuses}, {"failureFrequency", "status", frequencyStatuses},
	}
	for _, check := range checks {
		def, err := object(defs, check.def)
		if err != nil {
			return fmt.Errorf("schema: $defs.%s: %w", check.def, err)
		}
		props, err := object(def, "properties")
		if err != nil {
			return fmt.Errorf("schema: $defs.%s: %w", check.def, err)
		}
		property, err := object(props, check.property)
		if err != nil {
			return fmt.Errorf("schema: $defs.%s.%s: %w", check.def, check.property, err)
		}
		if err := requireEnum(property, check.values); err != nil {
			return fmt.Errorf("schema: $defs.%s.%s: %w", check.def, check.property, err)
		}
	}
	entry, _ := object(defs, "entry")
	entryProps, _ := object(entry, "properties")
	metrics, err := object(entryProps, "metrics")
	if err != nil {
		return fmt.Errorf("schema: $defs.entry.metrics: %w", err)
	}
	if number(metrics["minItems"]) != 3 || number(metrics["maxItems"]) != 3 {
		return fmt.Errorf("schema: metrics must fix exactly three items")
	}
	prefix, ok := metrics["prefixItems"].([]any)
	if !ok || len(prefix) != 3 {
		return fmt.Errorf("schema: metrics must define three prefixItems")
	}
	refs := []string{"#/$defs/tokenMetric", "#/$defs/toolMetric", "#/$defs/reviewMetric"}
	for i, want := range refs {
		item, ok := prefix[i].(map[string]any)
		if !ok {
			return fmt.Errorf("schema: metric prefix %d invalid", i)
		}
		if err := requireString(item, "$ref", want); err != nil {
			return fmt.Errorf("schema: metric prefix %d: %w", i, err)
		}
	}
	metric, _ := object(defs, "metric")
	if err := requireConditionalStatuses(metric, metricStatuses); err != nil {
		return fmt.Errorf("schema: metric: %w", err)
	}
	trace, _ := object(defs, "trace")
	if err := requireConditionalStatuses(trace, traceStatuses); err != nil {
		return fmt.Errorf("schema: trace: %w", err)
	}
	frequency, _ := object(defs, "failureFrequency")
	if err := requireConditionalStatuses(frequency, frequencyStatuses); err != nil {
		return fmt.Errorf("schema: failureFrequency: %w", err)
	}
	for _, check := range []struct {
		definition string
		wantRef    string
	}{
		{"tokenMetric", "#/$defs/sumCalculation"},
		{"toolMetric", "#/$defs/sumCalculation"},
		{"reviewMetric", "#/$defs/elapsedMinutesCalculation"},
	} {
		definition, err := object(defs, check.definition)
		if err != nil {
			return fmt.Errorf("schema: $defs.%s: %w", check.definition, err)
		}
		if err := requireDerivedCalculationRef(definition, check.wantRef); err != nil {
			return fmt.Errorf("schema: $defs.%s: %w", check.definition, err)
		}
	}
	for _, definitionName := range []string{"tokenMetric", "toolMetric"} {
		definition, _ := object(defs, definitionName)
		if err := requireMetricMaximum(definition, maxCount); err != nil {
			return fmt.Errorf("schema: $defs.%s: %w", definitionName, err)
		}
	}
	sumCalculation, _ := object(defs, "sumCalculation")
	sumProperties, err := object(sumCalculation, "properties")
	if err != nil {
		return fmt.Errorf("schema: $defs.sumCalculation: %w", err)
	}
	operands, err := object(sumProperties, "operands")
	if err != nil {
		return fmt.Errorf("schema: $defs.sumCalculation.operands: %w", err)
	}
	items, err := object(operands, "items")
	if err != nil {
		return fmt.Errorf("schema: $defs.sumCalculation.operands: %w", err)
	}
	if err := requireSchemaNumber(items, "maximum", maxCount); err != nil {
		return fmt.Errorf("schema: $defs.sumCalculation.operands: %w", err)
	}
	return nil
}

func number(v any) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	if n, ok := v.(json.Number); ok {
		parsed, err := strconv.Atoi(n.String())
		if err == nil {
			return parsed
		}
	}
	return -1
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("coding-agent work index contains multiple JSON values")
	}
	return fmt.Errorf("decode trailing coding-agent work index data: %w", err)
}

func validateIndex(root string, index map[string]any) error {
	if err := validateRepositoryPath(root, schemaPath); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if err := rejectUnsafeContent(index); err != nil {
		return err
	}
	if err := requireKeys(index, "$schema", "schema_version", "index_version", "entries", "failure_modes"); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if err := requireString(index, "$schema", schemaPath); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if err := requireString(index, "schema_version", schemaVersion); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if v, ok := index["index_version"].(json.Number); !ok || v.String() != "1" {
		return fmt.Errorf("index: unsupported index_version")
	}
	entries, err := objectArray(index, "entries")
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("index: entries must not be empty")
	}
	evidenceIDs, entryIDs, seenClasses := map[string]bool{}, map[string]bool{}, map[string]bool{}
	previous := ""
	for pos, entry := range entries {
		id, err := validateEntry(root, entry, evidenceIDs)
		if err != nil {
			return fmt.Errorf("entry %d: %w", pos, err)
		}
		if entryIDs[id] {
			return fmt.Errorf("entry %d: duplicate id %q", pos, id)
		}
		if previous >= id {
			return fmt.Errorf("entry %d: ids must be lexicographically ordered", pos)
		}
		entryIDs[id], seenClasses[entry["task_class"].(string)], previous = true, true, id
	}
	for class := range taskClasses {
		if !seenClasses[class] {
			return fmt.Errorf("index: missing representative task class %q", class)
		}
	}
	failureModes, err := objectArray(index, "failure_modes")
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if len(failureModes) == 0 {
		return fmt.Errorf("index: failure_modes must not be empty")
	}
	previous = ""
	failureIDs := map[string]bool{}
	for pos, mode := range failureModes {
		id, err := validateFailureMode(mode, evidenceIDs, entryIDs)
		if err != nil {
			return fmt.Errorf("failure mode %d: %w", pos, err)
		}
		if failureIDs[id] {
			return fmt.Errorf("failure mode %d: duplicate id %q", pos, id)
		}
		if previous >= id {
			return fmt.Errorf("failure mode %d: ids must be lexicographically ordered", pos)
		}
		failureIDs[id], previous = true, id
	}
	return nil
}

func validateEntry(root string, entry map[string]any, evidenceIDs map[string]bool) (string, error) {
	if err := requireKeys(entry, "id", "task_class", "title", "scope", "outcome", "evidence", "trace", "metrics"); err != nil {
		return "", err
	}
	id, err := identifier(entry, "id")
	if err != nil {
		return "", err
	}
	class, err := nonEmptyString(entry, "task_class")
	if err != nil {
		return "", err
	}
	if !taskClasses[class] {
		return "", fmt.Errorf("unsupported task_class %q", class)
	}
	for _, f := range []string{"title", "scope"} {
		if _, err := nonEmptyString(entry, f); err != nil {
			return "", err
		}
	}
	if err := validateOutcome(entry); err != nil {
		return "", err
	}
	localEvidence, err := validateEvidence(root, entry, evidenceIDs)
	if err != nil {
		return "", err
	}
	if err := validateTrace(entry, localEvidence); err != nil {
		return "", err
	}
	if err := validateMetrics(entry, localEvidence); err != nil {
		return "", err
	}
	return id, nil
}

func validateOutcome(entry map[string]any) error {
	o, err := object(entry, "outcome")
	if err != nil {
		return err
	}
	if err := requireKeys(o, "status", "summary"); err != nil {
		return fmt.Errorf("outcome: %w", err)
	}
	status, err := nonEmptyString(o, "status")
	if err != nil || !outcomeStatuses[status] {
		return fmt.Errorf("outcome: unsupported status %q", status)
	}
	if _, err := nonEmptyString(o, "summary"); err != nil {
		return fmt.Errorf("outcome: %w", err)
	}
	return nil
}

func validateEvidence(root string, entry map[string]any, global map[string]bool) (map[string]bool, error) {
	items, err := objectArray(entry, "evidence")
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("evidence must not be empty")
	}
	local := map[string]bool{}
	previous := ""
	for pos, item := range items {
		if err := requireKeys(item, "id", "kind", "repo_relative_path", "description"); err != nil {
			return nil, fmt.Errorf("evidence %d: %w", pos, err)
		}
		id, err := identifier(item, "id")
		if err != nil {
			return nil, fmt.Errorf("evidence %d: %w", pos, err)
		}
		if global[id] {
			return nil, fmt.Errorf("evidence %d: duplicate id %q", pos, id)
		}
		if previous >= id {
			return nil, fmt.Errorf("evidence %d: ids must be lexicographically ordered", pos)
		}
		kind, err := nonEmptyString(item, "kind")
		if err != nil || !evidenceKinds[kind] {
			return nil, fmt.Errorf("evidence %d: unsupported kind %q", pos, kind)
		}
		path, err := nonEmptyString(item, "repo_relative_path")
		if err != nil {
			return nil, fmt.Errorf("evidence %d: %w", pos, err)
		}
		if err := validateRepositoryPath(root, path); err != nil {
			return nil, fmt.Errorf("evidence %d: %w", pos, err)
		}
		if _, err := nonEmptyString(item, "description"); err != nil {
			return nil, fmt.Errorf("evidence %d: %w", pos, err)
		}
		global[id], local[id], previous = true, true, id
	}
	return local, nil
}

func validateRepositoryPath(root, path string) error {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path || filepath.ToSlash(path) != path {
		return fmt.Errorf("repo_relative_path %q is not a clean repository-relative path", path)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer r.Close()
	f, err := r.Open(path)
	if err != nil {
		return fmt.Errorf("repo_relative_path %q does not resolve within repository: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("repo_relative_path %q cannot be inspected: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("repo_relative_path %q must be a regular repository file", path)
	}
	return nil
}

func validateTrace(entry map[string]any, evidenceIDs map[string]bool) error {
	trace, err := object(entry, "trace")
	if err != nil {
		return err
	}
	status, err := nonEmptyString(trace, "status")
	if err != nil || !traceStatuses[status] {
		return fmt.Errorf("trace: unsupported status %q", status)
	}
	if status == "unavailable" {
		if err := requireKeys(trace, "status", "reason"); err != nil {
			return fmt.Errorf("trace: %w", err)
		}
		_, err := nonEmptyString(trace, "reason")
		return err
	}
	if err := requireKeys(trace, "status", "source_evidence_ids", "source", "actions"); err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	if err := validateEvidenceReferences(trace, "source_evidence_ids", evidenceIDs); err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	source, err := object(trace, "source")
	if err != nil {
		return fmt.Errorf("trace: %w", err)
	}
	if err := requireKeys(source, "record_type", "ledger_sha256", "sequence_first", "sequence_last", "derivation"); err != nil {
		return fmt.Errorf("trace source: %w", err)
	}
	if err := requireString(source, "record_type", "structured_context_event_ledger"); err != nil {
		return fmt.Errorf("trace source: %w", err)
	}
	digest, err := nonEmptyString(source, "ledger_sha256")
	if err != nil || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(digest) {
		return fmt.Errorf("trace source: ledger_sha256 must be a SHA-256 digest")
	}
	first, err := nonNegativeInteger(source, "sequence_first")
	if err != nil || first < 1 {
		return fmt.Errorf("trace source: sequence_first must be positive")
	}
	last, err := nonNegativeInteger(source, "sequence_last")
	if err != nil || last < first {
		return fmt.Errorf("trace source: sequence_last must not precede sequence_first")
	}
	if _, err := nonEmptyString(source, "derivation"); err != nil {
		return fmt.Errorf("trace source: %w", err)
	}
	actions, err := objectArray(trace, "actions")
	if err != nil || len(actions) == 0 {
		return fmt.Errorf("trace: actions must be a non-empty array")
	}
	for pos, action := range actions {
		if err := requireKeys(action, "order", "phase", "action_summary", "tool_categories"); err != nil {
			return fmt.Errorf("trace action %d: %w", pos, err)
		}
		order, err := nonNegativeInteger(action, "order")
		if err != nil || order != int64(pos+1) {
			return fmt.Errorf("trace action %d: order must be %d", pos, pos+1)
		}
		for _, f := range []string{"phase", "action_summary"} {
			if _, err := nonEmptyString(action, f); err != nil {
				return fmt.Errorf("trace action %d: %w", pos, err)
			}
		}
		categories, err := stringArray(action, "tool_categories")
		if err != nil || len(categories) == 0 || !strictlySorted(categories) {
			return fmt.Errorf("trace action %d: tool_categories must be non-empty and sorted", pos)
		}
	}
	return nil
}

func validateMetrics(entry map[string]any, evidenceIDs map[string]bool) error {
	metrics, err := objectArray(entry, "metrics")
	if err != nil {
		return err
	}
	if len(metrics) != 3 {
		return fmt.Errorf("metrics must contain exactly 3 metrics")
	}
	for pos, expected := range []string{"token_count", "tool_calls", "review_time"} {
		metric := metrics[pos]
		if err := requireKeysWithOptional(metric, []string{"name", "status", "unit", "provenance"}, "value"); err != nil {
			return fmt.Errorf("metric %d: %w", pos, err)
		}
		name, err := nonEmptyString(metric, "name")
		if err != nil || name != expected {
			return fmt.Errorf("metric %d: expected %q, got %q", pos, expected, name)
		}
		status, err := nonEmptyString(metric, "status")
		if err != nil || !metricStatuses[status] {
			return fmt.Errorf("metric %d: unsupported status %q", pos, status)
		}
		if err := requireString(metric, "unit", metricUnits[name]); err != nil {
			return fmt.Errorf("metric %d: %w", pos, err)
		}
		provenance, err := object(metric, "provenance")
		if err != nil {
			return fmt.Errorf("metric %d: provenance: %w", pos, err)
		}
		if err := validateEvidenceReferences(provenance, "evidence_ids", evidenceIDs); err != nil {
			return fmt.Errorf("metric %d: provenance: %w", pos, err)
		}
		value, hasValue := metric["value"]
		switch status {
		case "exact":
			if err := requireKeys(provenance, "evidence_ids", "measurement"); err != nil {
				return fmt.Errorf("metric %d: exact provenance: %w", pos, err)
			}
			if _, err := nonEmptyString(provenance, "measurement"); err != nil {
				return fmt.Errorf("metric %d: exact provenance: %w", pos, err)
			}
		case "derived":
			if err := requireKeys(provenance, "evidence_ids", "calculation"); err != nil {
				return fmt.Errorf("metric %d: derived provenance: %w", pos, err)
			}
		default:
			if err := requireKeys(provenance, "evidence_ids", "reason"); err != nil {
				return fmt.Errorf("metric %d: %s provenance: %w", pos, status, err)
			}
			if _, err := nonEmptyString(provenance, "reason"); err != nil {
				return fmt.Errorf("metric %d: %s provenance: %w", pos, status, err)
			}
		}
		if status == "exact" || status == "derived" {
			if !hasValue {
				return fmt.Errorf("metric %d: %s metrics require value", pos, status)
			}
			n, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("metric %d: value must be non-negative", pos)
			}
			if name == "review_time" {
				parsed, err := strconv.ParseFloat(n.String(), 64)
				if err != nil || parsed < 0 {
					return fmt.Errorf("metric %d: value must be non-negative", pos)
				}
				if status == "derived" {
					if err := validateReviewCalculation(parsed, provenance); err != nil {
						return fmt.Errorf("metric %d: %w", pos, err)
					}
				}
			} else {
				parsed, err := strconv.ParseUint(n.String(), 10, 64)
				if err != nil {
					return fmt.Errorf("metric %d: %s value must be a non-negative integer", pos, name)
				}
				if status == "derived" {
					if err := validateCountCalculation(parsed, provenance); err != nil {
						return fmt.Errorf("metric %d: %w", pos, err)
					}
				}
			}
		} else if hasValue {
			return fmt.Errorf("metric %d: %s metrics must not include value", pos, status)
		}
	}
	return nil
}

func validateReviewCalculation(value float64, provenance map[string]any) error {
	calculation, err := object(provenance, "calculation")
	if err != nil {
		return fmt.Errorf("derived provenance: %w", err)
	}
	if err := requireKeys(calculation, "operation", "started_at", "ended_at", "decimal_places"); err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	if err := requireString(calculation, "operation", "elapsed_minutes"); err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	startedValue, err := nonEmptyString(calculation, "started_at")
	if err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	started, err := time.Parse(time.RFC3339, startedValue)
	if err != nil {
		return fmt.Errorf("calculation: started_at must be RFC3339")
	}
	endedValue, err := nonEmptyString(calculation, "ended_at")
	if err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	ended, err := time.Parse(time.RFC3339, endedValue)
	if err != nil || ended.Before(started) {
		return fmt.Errorf("calculation: ended_at must be RFC3339 and not precede started_at")
	}
	places, err := nonNegativeInteger(calculation, "decimal_places")
	if err != nil || places > 6 {
		return fmt.Errorf("calculation: decimal_places must be an integer from 0 through 6")
	}
	factor := 1.0
	for range places {
		factor *= 10
	}
	expected := float64(int64(ended.Sub(started).Minutes()*factor+0.5)) / factor
	if value != expected {
		return fmt.Errorf("calculation result %v does not match value %v", expected, value)
	}
	return nil
}

func validateCountCalculation(value uint64, provenance map[string]any) error {
	calculation, err := object(provenance, "calculation")
	if err != nil {
		return fmt.Errorf("derived provenance: %w", err)
	}
	if err := requireKeys(calculation, "operation", "operands"); err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	if err := requireString(calculation, "operation", "sum"); err != nil {
		return fmt.Errorf("calculation: %w", err)
	}
	operands, ok := calculation["operands"].([]any)
	if !ok || len(operands) == 0 {
		return fmt.Errorf("calculation: operands must be a non-empty integer array")
	}
	var expected uint64
	for _, operand := range operands {
		number, ok := operand.(json.Number)
		if !ok {
			return fmt.Errorf("calculation: operands must be non-negative integers")
		}
		parsed, err := strconv.ParseUint(number.String(), 10, 64)
		if err != nil {
			return fmt.Errorf("calculation: operands must be non-negative integers")
		}
		if math.MaxUint64-expected < parsed {
			return fmt.Errorf("calculation: sum overflows uint64")
		}
		expected += parsed
	}
	if value != expected {
		return fmt.Errorf("calculation result %d does not match value %v", expected, value)
	}
	return nil
}

func validateFailureMode(mode map[string]any, evidenceIDs, entryIDs map[string]bool) (string, error) {
	if err := requireKeys(mode, "id", "title", "description", "observed_task_ids", "frequency"); err != nil {
		return "", err
	}
	id, err := identifier(mode, "id")
	if err != nil {
		return "", err
	}
	for _, f := range []string{"title", "description"} {
		if _, err := nonEmptyString(mode, f); err != nil {
			return "", err
		}
	}
	tasks, err := stringArray(mode, "observed_task_ids")
	if err != nil || len(tasks) == 0 || !strictlySorted(tasks) {
		return "", fmt.Errorf("observed_task_ids must be non-empty and sorted")
	}
	for _, task := range tasks {
		if !entryIDs[task] {
			return "", fmt.Errorf("observed_task_ids references unknown task %q", task)
		}
	}
	frequency, err := object(mode, "frequency")
	if err != nil {
		return "", err
	}
	status, err := nonEmptyString(frequency, "status")
	if err != nil || !frequencyStatuses[status] {
		return "", fmt.Errorf("frequency has unsupported status %q", status)
	}
	if status == "sample_observed" {
		if err := requireKeys(frequency, "status", "value", "unit", "calculation", "evidence_ids"); err != nil {
			return "", fmt.Errorf("frequency: %w", err)
		}
		value, err := nonNegativeInteger(frequency, "value")
		if err != nil || value != int64(len(tasks)) {
			return "", fmt.Errorf("frequency value must equal observed_task_ids length")
		}
		if err := requireString(frequency, "unit", "tasks"); err != nil {
			return "", fmt.Errorf("frequency: %w", err)
		}
		if _, err := nonEmptyString(frequency, "calculation"); err != nil {
			return "", fmt.Errorf("frequency: %w", err)
		}
	} else {
		if err := requireKeys(frequency, "status", "reason", "evidence_ids"); err != nil {
			return "", fmt.Errorf("frequency: %w", err)
		}
		if _, err := nonEmptyString(frequency, "reason"); err != nil {
			return "", fmt.Errorf("frequency: %w", err)
		}
	}
	if err := validateEvidenceReferences(frequency, "evidence_ids", evidenceIDs); err != nil {
		return "", fmt.Errorf("frequency: %w", err)
	}
	return id, nil
}

func validateEvidenceReferences(value map[string]any, field string, available map[string]bool) error {
	refs, err := stringArray(value, field)
	if err != nil || len(refs) == 0 {
		return fmt.Errorf("%s must be a non-empty string array", field)
	}
	if !strictlySorted(refs) {
		return fmt.Errorf("%s must be sorted", field)
	}
	for _, ref := range refs {
		if !available[ref] {
			return fmt.Errorf("%s references unknown evidence %q", field, ref)
		}
	}
	return nil
}

func strictlySorted(values []string) bool {
	return sort.StringsAreSorted(values) && len(values) == len(set(values...))
}

func rejectUnsafeContent(value any) error {
	switch v := value.(type) {
	case map[string]any:
		for key, nested := range v {
			if unsafeFields[normalizeField(key)] {
				return fmt.Errorf("unsafe field %q is forbidden", key)
			}
			if err := rejectUnsafeContent(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range v {
			if err := rejectUnsafeContent(nested); err != nil {
				return err
			}
		}
	case string:
		for _, pattern := range sensitiveValues {
			if pattern.MatchString(v) {
				return fmt.Errorf("sensitive value matching %q is forbidden", pattern.String())
			}
		}
	}
	return nil
}

func normalizeField(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
}
func requireEnum(value map[string]any, expected map[string]bool) error {
	items, ok := value["enum"].([]any)
	if !ok || len(items) != len(expected) {
		return fmt.Errorf("enum differs from validator constants")
	}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || !expected[s] {
			return fmt.Errorf("enum differs from validator constants")
		}
	}
	return nil
}

func requireConditionalStatuses(value map[string]any, expected map[string]bool) error {
	conditionals, ok := value["allOf"].([]any)
	if !ok || len(conditionals) != len(expected) {
		return fmt.Errorf("must define one status conditional per status")
	}
	seen := map[string]bool{}
	for _, conditionalValue := range conditionals {
		conditional, ok := conditionalValue.(map[string]any)
		if !ok {
			return fmt.Errorf("status conditional must be an object")
		}
		if _, err := object(conditional, "then"); err != nil {
			return fmt.Errorf("status conditional: %w", err)
		}
		ifObject, err := object(conditional, "if")
		if err != nil {
			return err
		}
		properties, err := object(ifObject, "properties")
		if err != nil {
			return err
		}
		status, err := object(properties, "status")
		if err != nil {
			return err
		}
		constant, err := nonEmptyString(status, "const")
		if err != nil || !expected[constant] || seen[constant] {
			return fmt.Errorf("status conditional constants differ from validator statuses")
		}
		seen[constant] = true
	}
	return nil
}

func requireDerivedCalculationRef(definition map[string]any, expected string) error {
	allOf, ok := definition["allOf"].([]any)
	if !ok || len(allOf) < 3 {
		return fmt.Errorf("must constrain derived calculation")
	}
	constraint, ok := allOf[2].(map[string]any)
	if !ok {
		return fmt.Errorf("derived calculation constraint must be an object")
	}
	ifObject, err := object(constraint, "if")
	if err != nil {
		return err
	}
	ifProperties, err := object(ifObject, "properties")
	if err != nil {
		return err
	}
	status, err := object(ifProperties, "status")
	if err != nil {
		return err
	}
	if err := requireString(status, "const", "derived"); err != nil {
		return err
	}
	then, err := object(constraint, "then")
	if err != nil {
		return err
	}
	properties, err := object(then, "properties")
	if err != nil {
		return err
	}
	provenance, err := object(properties, "provenance")
	if err != nil {
		return err
	}
	provenanceProperties, err := object(provenance, "properties")
	if err != nil {
		return err
	}
	calculation, err := object(provenanceProperties, "calculation")
	if err != nil {
		return err
	}
	return requireString(calculation, "$ref", expected)
}

func requireMetricMaximum(definition map[string]any, expected string) error {
	allOf, ok := definition["allOf"].([]any)
	if !ok || len(allOf) < 2 {
		return fmt.Errorf("must define a metric value constraint")
	}
	constraint, ok := allOf[1].(map[string]any)
	if !ok {
		return fmt.Errorf("metric value constraint must be an object")
	}
	properties, err := object(constraint, "properties")
	if err != nil {
		return err
	}
	value, err := object(properties, "value")
	if err != nil {
		return err
	}
	return requireSchemaNumber(value, "maximum", expected)
}

func requireSchemaNumber(value map[string]any, field, expected string) error {
	number, ok := value[field].(json.Number)
	if !ok || number.String() != expected {
		return fmt.Errorf("field %q must equal %s", field, expected)
	}
	return nil
}
func nonNegativeInteger(value map[string]any, field string) (int64, error) {
	n, ok := value[field].(json.Number)
	if !ok {
		return 0, fmt.Errorf("field %q must be an integer", field)
	}
	parsed, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("field %q must be a non-negative integer", field)
	}
	return parsed, nil
}
func requireKeys(value map[string]any, required ...string) error {
	return requireKeysWithOptional(value, required)
}
func requireKeysWithOptional(value map[string]any, required []string, optional ...string) error {
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		if _, ok := value[key]; !ok {
			return fmt.Errorf("missing required field %q", key)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range value {
		if !allowed[key] {
			return fmt.Errorf("unsupported field %q", key)
		}
	}
	return nil
}
func object(value map[string]any, field string) (map[string]any, error) {
	v, ok := value[field].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an object", field)
	}
	return v, nil
}
func objectArray(value map[string]any, field string) ([]map[string]any, error) {
	values, ok := value[field].([]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an array", field)
	}
	result := make([]map[string]any, 0, len(values))
	for pos, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field %q item %d must be an object", field, pos)
		}
		result = append(result, item)
	}
	return result, nil
}
func stringArray(value map[string]any, field string) ([]string, error) {
	values, ok := value[field].([]any)
	if !ok {
		return nil, fmt.Errorf("field %q must be an array", field)
	}
	result := make([]string, 0, len(values))
	for pos, value := range values {
		item, ok := value.(string)
		if !ok || strings.TrimSpace(item) == "" {
			return nil, fmt.Errorf("field %q item %d must be non-empty", field, pos)
		}
		result = append(result, item)
	}
	return result, nil
}
func identifier(value map[string]any, field string) (string, error) {
	id, err := nonEmptyString(value, field)
	if err != nil {
		return "", err
	}
	valid := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	if !valid.MatchString(id) {
		return "", fmt.Errorf("field %q has invalid identifier %q", field, id)
	}
	return id, nil
}
func nonEmptyString(value map[string]any, field string) (string, error) {
	s, ok := value[field].(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("field %q must be a non-empty string", field)
	}
	return s, nil
}
func requireString(value map[string]any, field, expected string) error {
	actual, err := nonEmptyString(value, field)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("field %q = %q, want %q", field, actual, expected)
	}
	return nil
}
