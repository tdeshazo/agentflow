// Package workflow provides the agentflow workflow specification and utilities.
// It includes types for the v1alpha1 workflow schema, loading and validation,
// and template expansion for runtime expression evaluation.
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Position is the location of a YAML value. Paths use map keys and zero-based
// sequence indexes, for example spec.phases[0].actor.
type Position struct{ Line, Column int }
type Locations map[string]Position

type sourceDiagnosticError struct {
	path     string
	position Position
	err      error
}

func (e *sourceDiagnosticError) Error() string { return e.err.Error() }
func (e *sourceDiagnosticError) Unwrap() error { return e.err }

var yamlErrorLine = regexp.MustCompile(`\bline ([0-9]+):`)

// Document pairs the executable model with its source locations. It is the
// single input to semantic validation; the validator never re-decodes YAML.
type Document struct {
	// Workflow is the shared executable projection. For v1alpha1 it is the
	// decoded document; for v1alpha2 it is produced from the version-specific
	// authoring representation.
	Workflow *Workflow
	// V1Alpha2 retains the authored form rather than pretending its concise
	// contract is a v1alpha1 document.
	V1Alpha2 *V1Alpha2Workflow
	// V1Alpha3 retains the typed-contract authoring form. It is separate from
	// v1alpha2 so adding contracts does not redefine a stable API version.
	V1Alpha3 *V1Alpha3Workflow
	// V1Alpha4 retains typed, engine-owned criteria and bounded collection
	// expansion separately from v1alpha3's contract grammar.
	V1Alpha4 *V1Alpha4Workflow
	// DependencyGraph is the normalized v1alpha2 dependency contract. It is
	// outside Phase so v1alpha1 continues to reject the unknown dependsOn field.
	DependencyGraph PhaseDependencyGraph
	// PhaseDependencies is retained for source compatibility. New consumers
	// must use DependencyGraph, which preserves order and acceptance semantics.
	PhaseDependencies map[string][]string
	Locations         Locations
}

type Status string

const (
	Invalid     Status = "invalid"
	Executable  Status = "executable"
	Unsupported Status = "unsupported"
)

type Diagnostic struct {
	Status   Status
	Path     string
	Position Position
	Message  string
}

func (d Diagnostic) String() string {
	where := d.Path
	if where == "" {
		where = "$"
	}
	if d.Position.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s: %s", where, d.Position.Line, d.Position.Column, d.Status, d.Message)
	}
	return fmt.Sprintf("%s: %s: %s", where, d.Status, d.Message)
}

// Result is the output of workflow validation, containing status and diagnostics.
type Result struct {
	Status   Status
	Document *Document
	// Normalized is the fully resolved executable form when authoring defaults
	// are used. It is retained for tooling; execution independently normalizes
	// at its construction boundary.
	Normalized  *Document
	Diagnostics []Diagnostic
}

// Decode dispatches from apiVersion before decoding the selected contract.
// v1alpha1 retains its existing concise authoring rewrite; v1alpha2 is decoded
// directly from the original bytes, retaining its separate authoring form.
// Every selected decoder uses KnownFields so executable spelling errors cannot
// be silently discarded by the Go runtime.
// Decode does no repository inspection or other workspace mutation.
func Decode(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("parse workflow YAML: %w", sourceAwareYAMLError(err, &root))
	}

	apiVersion, err := documentAPIVersion(&root)
	if err != nil {
		return nil, fmt.Errorf("decode workflow: %w", err)
	}
	locations := indexLocations(&root)
	file, _ := filepath.Abs(path)

	switch apiVersion {
	case "agentflow.dev/v1alpha1":
		decodeBytes := b
		if rewritten, changed, err := rewriteConciseAuthoring(&root); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		} else if changed {
			decodeBytes = rewritten
		}
		var w Workflow
		if err := decodeKnownBytes(decodeBytes, &w); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		w.File = file
		return &Document{Workflow: &w, Locations: locations}, nil
	case "agentflow.dev/v1alpha2":
		if err := rejectV1Alpha2MergeKeys(&root); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		var authored V1Alpha2Workflow
		if err := decodeKnownBytes(b, &authored); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		authored.File = file
		normalized, err := normalizeV1Alpha2(&authored, locations)
		if err != nil {
			return nil, fmt.Errorf("decode workflow: %w", err)
		}
		normalized.V1Alpha2 = &authored
		return normalized, nil
	case "agentflow.dev/v1alpha3":
		if err := rejectV1Alpha2MergeKeys(&root); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		var authored V1Alpha3Workflow
		if err := decodeKnownBytes(b, &authored); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		authored.File = file
		normalized, err := normalizeV1Alpha3(&authored, locations)
		if err != nil {
			return nil, fmt.Errorf("decode workflow: %w", err)
		}
		return normalized, nil
	case "agentflow.dev/v1alpha4":
		if err := rejectV1Alpha2MergeKeys(&root); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		var authored V1Alpha4Workflow
		if err := decodeKnownBytes(b, &authored); err != nil {
			return nil, fmt.Errorf("decode workflow: %w", sourceAwareYAMLError(err, &root))
		}
		authored.File = file
		normalized, err := normalizeV1Alpha4(&authored, locations)
		if err != nil {
			return nil, fmt.Errorf("decode workflow: %w", err)
		}
		return normalized, nil
	default:
		return nil, fmt.Errorf("decode workflow: unsupported apiVersion %q", apiVersion)
	}
}

func decodeKnownBytes(b []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(out)
}

func documentAPIVersion(root *yaml.Node) (string, error) {
	doc := documentMapping(root)
	if doc == nil {
		return "", fmt.Errorf("workflow document must be a mapping")
	}
	value, ok := mappingValue(doc, "apiVersion")
	if !ok {
		return "", fmt.Errorf("apiVersion is required")
	}
	apiVersion, ok := scalarValueFollowingAliases(value)
	if !ok {
		return "", fmt.Errorf("apiVersion must be a string")
	}
	return apiVersion, nil
}

// Load remains the programmatic loading entry point. Call Validate when an
// executable workflow is required; this split lets tooling inspect a document
// without constructing an engine.
func Load(path string) (*Workflow, error) {
	d, err := Decode(path)
	if err != nil {
		return nil, err
	}
	result := Validate(d)
	if result.Status == Invalid {
		if len(result.Diagnostics) == 0 {
			return nil, fmt.Errorf("invalid workflow")
		}
		return nil, fmt.Errorf("invalid workflow: %s", result.Diagnostics[0])
	}
	return d.Workflow, nil
}

// ValidateFile reads and validates a workflow file, returning a Result with diagnostics.
// It does not require the workflow to be executable.
func ValidateFile(path string) Result {
	d, err := Decode(path)
	if err != nil {
		diagnostic := Diagnostic{Status: Invalid, Message: err.Error()}
		var sourceErr *sourceDiagnosticError
		if errors.As(err, &sourceErr) {
			diagnostic.Path = sourceErr.path
			diagnostic.Position = sourceErr.position
		}
		return Result{Status: Invalid, Diagnostics: []Diagnostic{diagnostic}}
	}
	return Validate(d)
}

func sourceAwareYAMLError(err error, root *yaml.Node) error {
	if err == nil {
		return nil
	}
	matches := yamlErrorLine.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return err
	}
	outer, parseErr := strconv.Atoi(matches[0][1])
	if parseErr != nil {
		return err
	}
	line := outer
	if len(matches) > 1 {
		inner, innerErr := strconv.Atoi(matches[len(matches)-1][1])
		if innerErr == nil {
			// Custom strict decoders marshal one YAML subtree before decoding it.
			// yaml.v3 reports the subtree-relative line after the original node line.
			line = outer + inner - 1
		}
	}
	path, position := yamlPathAtLine(root, line)
	if position.Line == 0 {
		position.Line = line
	}
	return &sourceDiagnosticError{path: path, position: position, err: err}
}

func yamlPathAtLine(root *yaml.Node, line int) (string, Position) {
	var walk func(*yaml.Node, string) (string, Position, bool)
	walk = func(node *yaml.Node, path string) (string, Position, bool) {
		if node == nil {
			return "", Position{}, false
		}
		switch node.Kind {
		case yaml.DocumentNode:
			for _, child := range node.Content {
				if foundPath, position, ok := walk(child, path); ok {
					return foundPath, position, true
				}
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				childPath := key.Value
				if path != "" {
					childPath = path + "." + key.Value
				}
				if key.Line == line {
					return childPath, Position{Line: key.Line, Column: key.Column}, true
				}
				if foundPath, position, ok := walk(value, childPath); ok {
					return foundPath, position, true
				}
			}
		case yaml.SequenceNode:
			for i, child := range node.Content {
				if foundPath, position, ok := walk(child, fmt.Sprintf("%s[%d]", path, i)); ok {
					return foundPath, position, true
				}
			}
		default:
			if node.Line == line {
				return path, Position{Line: node.Line, Column: node.Column}, true
			}
		}
		return "", Position{}, false
	}
	path, position, _ := walk(root, "")
	return path, position
}

func indexLocations(root *yaml.Node) Locations {
	loc := Locations{}
	if root == nil || len(root.Content) == 0 {
		return loc
	}
	var walk func(*yaml.Node, string)
	walk = func(n *yaml.Node, path string) {
		if path != "" {
			loc[path] = Position{Line: n.Line, Column: n.Column}
		}
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, path)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i], n.Content[i+1]
				child := key.Value
				if path != "" {
					child = path + "." + child
				}
				walk(value, child)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(root, "")
	return loc
}
