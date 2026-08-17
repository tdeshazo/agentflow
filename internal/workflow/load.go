// Package workflow provides the agentflow workflow specification and utilities.
// It includes types for the v1alpha1 workflow schema, loading and validation,
// and template expansion for runtime expression evaluation.
package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Position is the location of a YAML value. Paths use map keys and zero-based
// sequence indexes, for example spec.phases[0].actor.
type Position struct{ Line, Column int }
type Locations map[string]Position

// Document pairs the executable model with its source locations. It is the
// single input to semantic validation; the validator never re-decodes YAML.
type Document struct {
	Workflow  *Workflow
	Locations Locations
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
	Status      Status
	Document    *Document
	Diagnostics []Diagnostic
}

// Decode uses KnownFields so an executable spelling error cannot be silently
// discarded by the Go runtime. Decode does no repository inspection or other
// workspace mutation.
func Decode(path string) (*Document, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("parse workflow YAML: %w", err)
	}
	var w Workflow
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("decode workflow: %w", err)
	}
	w.File, _ = filepath.Abs(path)
	return &Document{Workflow: &w, Locations: indexLocations(&root)}, nil
}

// Load remains the programmatic loading entry point. Call Validate when an
// executable workflow is required; this split lets tooling inspect a
// spec-valid but runtime-unsupported document without constructing an engine.
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
		return Result{Status: Invalid, Diagnostics: []Diagnostic{{Status: Invalid, Message: err.Error()}}}
	}
	return Validate(d)
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
