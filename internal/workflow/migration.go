package workflow

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// MigrationClass is the deterministic disposition of a v1alpha1 construct.
type MigrationClass string

const (
	DirectSuccessorCapability MigrationClass = "direct-successor-capability"
	RuntimeOwnedEquivalent    MigrationClass = "runtime-owned-equivalent"
	GeneralizedReplacement    MigrationClass = "generalized-replacement"
	CompatibilityOnly         MigrationClass = "compatibility-only"
)

// MigrationMatrix is the checked-in, machine-readable v1alpha1 maintenance
// contract. FieldPatterns use * for a single map key and ** for any suffix.
type MigrationMatrix struct {
	SchemaVersion   int                       `json:"schemaVersion"`
	APIVersion      string                    `json:"apiVersion"`
	Status          string                    `json:"status"`
	Classifications map[MigrationClass]string `json:"classifications"`
	Capabilities    []MigrationCapability     `json:"capabilities"`
}

type MigrationCapability struct {
	ID             string         `json:"id"`
	Classification MigrationClass `json:"classification"`
	FieldPatterns  []string       `json:"fieldPatterns"`
	Successor      string         `json:"successor"`
	Note           string         `json:"note"`
}

// MigrationDiagnostic classifies one authored v1alpha1 field without
// rewriting or executing the workflow.
type MigrationDiagnostic struct {
	Path           string         `yaml:"path"`
	Line           int            `yaml:"line"`
	Column         int            `yaml:"column"`
	Classification MigrationClass `yaml:"classification"`
	Successor      string         `yaml:"successor"`
	Note           string         `yaml:"note"`
}

type MigrationReport struct {
	SchemaVersion int                   `yaml:"schemaVersion"`
	Source        string                `yaml:"source"`
	APIVersion    string                `yaml:"apiVersion"`
	Status        string                `yaml:"status"`
	Diagnostics   []MigrationDiagnostic `yaml:"diagnostics"`
}

//go:embed v1alpha1_migration_matrix.json
var migrationMatrixJSON []byte

var (
	migrationMatrixOnce sync.Once
	migrationMatrix     MigrationMatrix
	migrationMatrixErr  error
)

// V1Alpha1MigrationMatrix returns the immutable capability matrix used by
// migrate --check. Keeping the data beside the checker makes a released CLI
// independent of its working directory.
func V1Alpha1MigrationMatrix() (MigrationMatrix, error) {
	migrationMatrixOnce.Do(func() {
		migrationMatrixErr = json.Unmarshal(migrationMatrixJSON, &migrationMatrix)
		if migrationMatrixErr != nil {
			return
		}
		if migrationMatrix.SchemaVersion != 1 || migrationMatrix.APIVersion != "agentflow.dev/v1alpha1" || migrationMatrix.Status != "supported-maintenance-frozen" {
			migrationMatrixErr = fmt.Errorf("invalid v1alpha1 migration matrix metadata")
			return
		}
		for _, capability := range migrationMatrix.Capabilities {
			if capability.ID == "" || capability.Classification == "" || len(capability.FieldPatterns) == 0 {
				migrationMatrixErr = fmt.Errorf("invalid v1alpha1 migration matrix capability")
				return
			}
			if _, ok := migrationMatrix.Classifications[capability.Classification]; !ok {
				migrationMatrixErr = fmt.Errorf("unknown v1alpha1 migration classification %q", capability.Classification)
				return
			}
		}
	})
	return migrationMatrix, migrationMatrixErr
}

// MigrationCheckFile validates and classifies a v1alpha1 workflow. It does
// not open a repository, invoke a provider, or alter the source document.
func MigrationCheckFile(path string) (MigrationReport, error) {
	result := ValidateFile(path)
	if result.Status == Invalid {
		return MigrationReport{}, fmt.Errorf("workflow is invalid: %s", diagnosticsErrorText(result.Diagnostics))
	}
	if result.Document == nil || result.Document.Workflow == nil || result.Document.Workflow.APIVersion != "agentflow.dev/v1alpha1" {
		return MigrationReport{}, fmt.Errorf("migrate --check requires an agentflow.dev/v1alpha1 workflow")
	}
	matrix, err := V1Alpha1MigrationMatrix()
	if err != nil {
		return MigrationReport{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return MigrationReport{}, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return MigrationReport{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	diagnostics := make([]MigrationDiagnostic, 0)
	collectMigrationDiagnostics(documentMapping(&root), "", matrix, &diagnostics)
	sort.SliceStable(diagnostics, func(i, j int) bool {
		if diagnostics[i].Line != diagnostics[j].Line {
			return diagnostics[i].Line < diagnostics[j].Line
		}
		if diagnostics[i].Column != diagnostics[j].Column {
			return diagnostics[i].Column < diagnostics[j].Column
		}
		return diagnostics[i].Path < diagnostics[j].Path
	})
	return MigrationReport{
		SchemaVersion: matrix.SchemaVersion,
		Source:        path,
		APIVersion:    result.Document.Workflow.APIVersion,
		Status:        matrix.Status,
		Diagnostics:   diagnostics,
	}, nil
}

func diagnosticsErrorText(diagnostics []Diagnostic) string {
	if len(diagnostics) == 0 {
		return "invalid workflow"
	}
	return diagnostics[0].String()
}

func collectMigrationDiagnostics(node *yaml.Node, path string, matrix MigrationMatrix, diagnostics *[]MigrationDiagnostic) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		collectMigrationDiagnostics(node.Alias, path, matrix, diagnostics)
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			childPath := key.Value
			if path != "" {
				childPath = path + "." + childPath
			}
			if value.Kind == yaml.MappingNode || value.Kind == yaml.SequenceNode {
				collectMigrationDiagnostics(value, childPath, matrix, diagnostics)
				continue
			}
			appendMigrationDiagnostic(childPath, key, matrix, diagnostics)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if child.Kind == yaml.MappingNode || child.Kind == yaml.SequenceNode {
				collectMigrationDiagnostics(child, childPath, matrix, diagnostics)
				continue
			}
			appendMigrationDiagnostic(childPath, child, matrix, diagnostics)
		}
	}
}

func appendMigrationDiagnostic(path string, node *yaml.Node, matrix MigrationMatrix, diagnostics *[]MigrationDiagnostic) {
	capability, ok := migrationCapabilityFor(path, matrix)
	if !ok {
		// Strict decoding has already established this as a supported field. An
		// unmatched path is therefore a matrix completeness defect, never an
		// ambiguous migration result.
		*diagnostics = append(*diagnostics, MigrationDiagnostic{Path: path, Line: node.Line, Column: node.Column, Classification: CompatibilityOnly, Successor: "none", Note: "matrix coverage missing"})
		return
	}
	*diagnostics = append(*diagnostics, MigrationDiagnostic{Path: path, Line: node.Line, Column: node.Column, Classification: capability.Classification, Successor: capability.Successor, Note: capability.Note})
}

var indexPath = regexp.MustCompile(`\[[0-9]+\]`)

func migrationCapabilityFor(path string, matrix MigrationMatrix) (MigrationCapability, bool) {
	normalized := indexPath.ReplaceAllString(path, "[]")
	bestLength := -1
	var best MigrationCapability
	for _, capability := range matrix.Capabilities {
		for _, pattern := range capability.FieldPatterns {
			if migrationPathMatches(pattern, normalized) && len(pattern) > bestLength {
				best, bestLength = capability, len(pattern)
			}
		}
	}
	return best, bestLength >= 0
}

func migrationPathMatches(pattern, path string) bool {
	patternParts := strings.Split(pattern, ".")
	pathParts := strings.Split(path, ".")
	for len(patternParts) > 0 {
		part := patternParts[0]
		patternParts = patternParts[1:]
		if part == "**" {
			return true
		}
		if len(pathParts) == 0 || (part != "*" && part != pathParts[0] && part != strings.TrimSuffix(pathParts[0], "[]")) {
			return false
		}
		pathParts = pathParts[1:]
	}
	return len(pathParts) == 0
}
