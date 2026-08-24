package workflow

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// UnmarshalYAML expands concise authoring spellings into the ordinary v1alpha1
// executable model before semantic validation. The rewrite is deliberately
// narrow: it only removes boilerplate and does not add a new authority domain.
// KnownFields validation still runs against the rewritten canonical Spec.
func (s *Spec) UnmarshalYAML(n *yaml.Node) error {
	rewritten := cloneYAMLNode(n)
	if err := expandWorkspaceAllowWrites(rewritten); err != nil {
		return err
	}
	if err := expandInlineValidationRuns(rewritten); err != nil {
		return err
	}

	type plain Spec
	var out plain
	if err := decodeKnownNode(rewritten, &out); err != nil {
		return err
	}
	*s = Spec(out)
	return nil
}

// workspace.allowWrites is shorthand for workspace.mutationPolicy.allowed.
// The two forms cannot be combined because merge behavior would make the
// effective mutation authority less obvious to a reviewer.
func expandWorkspaceAllowWrites(spec *yaml.Node) error {
	workspace, ok := mappingValue(spec, "workspace")
	if !ok {
		return nil
	}
	if workspace.Kind != yaml.MappingNode {
		return nil // Let the canonical decoder report the structural error.
	}
	allowWrites, ok := mappingValue(workspace, "allowWrites")
	if !ok {
		return nil
	}

	mutationPolicy, hasPolicy := mappingValue(workspace, "mutationPolicy")
	if hasPolicy && mutationPolicy.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: workspace.mutationPolicy must be a mapping when workspace.allowWrites is used", mutationPolicy.Line)
	}
	if hasPolicy {
		if _, hasAllowed := mappingValue(mutationPolicy, "allowed"); hasAllowed {
			return fmt.Errorf("line %d: workspace must not declare both allowWrites and mutationPolicy.allowed", allowWrites.Line)
		}
	} else {
		mutationPolicy = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: workspace.Line, Column: workspace.Column}
		appendMappingField(workspace, "mutationPolicy", mutationPolicy)
	}

	removeMappingField(workspace, "allowWrites")
	appendMappingField(mutationPolicy, "allowed", allowWrites)
	return nil
}

// validation.<name>.run is shorthand for a one-step shell validation. It is
// compiled to an internal shell tool plus the normal validation.steps form so
// the engine, evidence keys, repair behavior, and expanded plan continue to use
// the existing executable contract.
func expandInlineValidationRuns(spec *yaml.Node) error {
	validations, ok := mappingValue(spec, "validation")
	if !ok || validations.Kind != yaml.MappingNode {
		return nil
	}

	tools, hasTools := mappingValue(spec, "tools")
	if hasTools && tools.Kind != yaml.MappingNode {
		return nil // Let the canonical decoder report the structural error.
	}
	if !hasTools {
		tools = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: validations.Line, Column: validations.Column}
		appendMappingField(spec, "tools", tools)
	}

	for i := 0; i+1 < len(validations.Content); i += 2 {
		nameNode, validation := validations.Content[i], validations.Content[i+1]
		if validation.Kind != yaml.MappingNode {
			continue
		}
		run, hasRun := mappingValue(validation, "run")
		if !hasRun {
			continue
		}
		if _, hasSteps := mappingValue(validation, "steps"); hasSteps {
			return fmt.Errorf("line %d: validation %q must not declare both run and steps", run.Line, nameNode.Value)
		}
		if run.Kind != yaml.ScalarNode || run.Tag != "!!str" || strings.TrimSpace(run.Value) == "" {
			return fmt.Errorf("line %d: validation %q run must be a non-empty string", run.Line, nameNode.Value)
		}

		toolName := "__inline_validation__" + nameNode.Value
		if _, exists := mappingValue(tools, toolName); exists {
			return fmt.Errorf("line %d: validation %q run conflicts with generated tool name %q", run.Line, nameNode.Value, toolName)
		}

		tool := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: run.Line, Column: run.Column}
		appendMappingScalar(tool, "type", "shell")
		appendMappingField(tool, "command", cloneYAMLNode(run))
		appendMappingField(tools, toolName, tool)

		step := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: run.Line, Column: run.Column}
		appendMappingScalar(step, "uses", toolName)
		steps := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Line: run.Line, Column: run.Column, Content: []*yaml.Node{step}}

		removeMappingField(validation, "run")
		appendMappingField(validation, "steps", steps)
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1], true
		}
	}
	return nil, false
}

func removeMappingField(mapping *yaml.Node, key string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func appendMappingField(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, Line: value.Line, Column: value.Column},
		value,
	)
}

func appendMappingScalar(mapping *yaml.Node, key, value string) {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Line: mapping.Line, Column: mapping.Column}
	appendMappingField(mapping, key, n)
}

func cloneYAMLNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	out := *n
	out.Content = make([]*yaml.Node, len(n.Content))
	for i, child := range n.Content {
		out.Content[i] = cloneYAMLNode(child)
	}
	return &out
}
