package workflow

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const inlineActorPrefix = "__inline_actor__"

// rewriteConciseAuthoring inspects the parsed document without changing the
// ordinary decode path. If no shorthand is present, it returns changed=false
// so Decode can feed the original file bytes to yaml.Decoder unchanged.
//
// When shorthand is present, only the YAML AST is rewritten. The result is
// serialized once to the canonical v1alpha1 shape and then decoded with the
// same KnownFields-enabled decoder as an ordinary workflow.
func rewriteConciseAuthoring(root *yaml.Node) ([]byte, bool, error) {
	doc := documentMapping(root)
	if doc == nil {
		return nil, false, nil
	}
	spec, ok := mappingValue(doc, "spec")
	if !ok || spec.Kind != yaml.MappingNode {
		return nil, false, nil // Let the canonical decoder report structure errors.
	}
	if err := rejectReservedInlineActorNamespace(spec); err != nil {
		return nil, false, err
	}
	if !usesConciseAuthoring(spec) {
		return nil, false, nil
	}

	rewrittenRoot := cloneYAMLNode(root)
	rewrittenDoc := documentMapping(rewrittenRoot)
	rewrittenSpec, _ := mappingValue(rewrittenDoc, "spec")
	if err := rejectMergeKey(rewrittenSpec, "spec"); err != nil {
		return nil, false, err
	}
	if err := expandWorkspaceAllowWrites(rewrittenSpec); err != nil {
		return nil, false, err
	}
	if err := expandInlinePhaseActors(rewrittenSpec); err != nil {
		return nil, false, err
	}
	if err := expandInlineValidationRuns(rewrittenSpec); err != nil {
		return nil, false, err
	}
	b, err := marshalYAMLNodePreservingFoldedScalars(rewrittenRoot)
	if err != nil {
		return nil, false, fmt.Errorf("encode concise authoring form: %w", err)
	}
	return b, true, nil
}

func usesConciseAuthoring(spec *yaml.Node) bool {
	if workspace, ok := mappingValue(spec, "workspace"); ok && workspace.Kind == yaml.MappingNode {
		if _, ok := mappingValue(workspace, "allowWrites"); ok {
			return true
		}
	}
	if phases, ok := mappingValue(spec, "phases"); ok && phases.Kind == yaml.SequenceNode {
		for _, phase := range phases.Content {
			if phase.Kind != yaml.MappingNode {
				continue
			}
			if actor, ok := mappingValue(phase, "actor"); ok && actor.Kind == yaml.MappingNode {
				return true
			}
		}
	}
	if validations, ok := mappingValue(spec, "validation"); ok && validations.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(validations.Content); i += 2 {
			validation := validations.Content[i+1]
			if validation.Kind != yaml.MappingNode {
				continue
			}
			if _, ok := mappingValue(validation, "run"); ok {
				return true
			}
		}
	}
	return false
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
	if err := rejectMergeKey(workspace, "workspace"); err != nil {
		return err
	}

	mutationPolicy, hasPolicy := mappingValue(workspace, "mutationPolicy")
	if hasPolicy && mutationPolicy.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: workspace.mutationPolicy must be a mapping when workspace.allowWrites is used", mutationPolicy.Line)
	}
	if hasPolicy {
		if err := rejectMergeKey(mutationPolicy, "workspace.mutationPolicy"); err != nil {
			return err
		}
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

// A mapping-valued phase.actor is shorthand for a one-off named agent. It is
// compiled into spec.agents under a reserved deterministic name and the phase
// is rewritten to reference that name. This keeps provider defaults, runtime
// lookup, validation, and execution on the same path as explicitly named
// agents instead of creating a second actor representation.
func expandInlinePhaseActors(spec *yaml.Node) error {
	phases, ok := mappingValue(spec, "phases")
	if !ok || phases.Kind != yaml.SequenceNode {
		return nil
	}

	var agents *yaml.Node
	for i, phase := range phases.Content {
		if phase.Kind != yaml.MappingNode {
			continue
		}
		actor, hasActor := mappingValue(phase, "actor")
		if !hasActor || actor.Kind != yaml.MappingNode {
			continue
		}
		if err := rejectMergeKey(phase, fmt.Sprintf("phases[%d]", i)); err != nil {
			return err
		}
		if err := rejectMergeKey(actor, fmt.Sprintf("phases[%d].actor", i)); err != nil {
			return err
		}

		if agents == nil {
			var hasAgents bool
			agents, hasAgents = mappingValue(spec, "agents")
			if hasAgents && agents.Kind != yaml.MappingNode {
				return nil // Let the canonical decoder report the structural error.
			}
			if hasAgents {
				if err := rejectMergeKey(agents, "agents"); err != nil {
					return err
				}
			} else {
				agents = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: phases.Line, Column: phases.Column}
				appendMappingField(spec, "agents", agents)
			}
		}

		phaseKey := fmt.Sprintf("phase_%d", i)
		if id, hasID := mappingValue(phase, "id"); hasID && id.Kind == yaml.ScalarNode && strings.TrimSpace(id.Value) != "" {
			phaseKey = id.Value
		}
		agentName := inlineActorPrefix + phaseKey
		if _, exists := mappingValue(agents, agentName); exists {
			return fmt.Errorf("line %d: inline actor for phase %q conflicts with generated agent name %q", actor.Line, phaseKey, agentName)
		}

		appendMappingField(agents, agentName, cloneYAMLNode(actor))
		removeMappingField(phase, "actor")
		actorRef := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: agentName, Line: actor.Line, Column: actor.Column}
		appendMappingField(phase, "actor", actorRef)
	}
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
	if !containsInlineValidationRun(validations) {
		return nil
	}
	if err := rejectMergeKey(validations, "validation"); err != nil {
		return err
	}

	tools, hasTools := mappingValue(spec, "tools")
	if hasTools && tools.Kind != yaml.MappingNode {
		return nil // Let the canonical decoder report the structural error.
	}
	if hasTools {
		if err := rejectMergeKey(tools, "tools"); err != nil {
			return err
		}
	} else {
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
		if err := rejectMergeKey(validation, "validation."+nameNode.Value); err != nil {
			return err
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

func containsInlineValidationRun(validations *yaml.Node) bool {
	for i := 0; i+1 < len(validations.Content); i += 2 {
		validation := validations.Content[i+1]
		if validation.Kind != yaml.MappingNode {
			continue
		}
		if _, ok := mappingValue(validation, "run"); ok {
			return true
		}
	}
	return false
}

// The inline actor namespace is implementation-owned. Authors may neither
// declare agents with the prefix nor refer to such names from authored actor
// references, including aliases and repair/default references. That prevents a
// generated one-off capability from becoming an externally addressable
// workflow API.
func rejectReservedInlineActorNamespace(spec *yaml.Node) error {
	if agents, ok := mappingValue(spec, "agents"); ok && agents.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(agents.Content); i += 2 {
			name := agents.Content[i]
			if value, ok := scalarValueFollowingAliases(name); ok && strings.HasPrefix(value, inlineActorPrefix) {
				return fmt.Errorf("line %d: agent name %q uses reserved prefix %q", name.Line, value, inlineActorPrefix)
			}
		}
	}

	if phases, ok := mappingValue(spec, "phases"); ok && phases.Kind == yaml.SequenceNode {
		for i, phase := range phases.Content {
			if phase.Kind != yaml.MappingNode {
				continue
			}
			if actor, ok := mappingValue(phase, "actor"); ok {
				if err := rejectReservedActorScalar(actor, fmt.Sprintf("phases[%d].actor", i)); err != nil {
					return err
				}
			}
		}
	}

	if defaults, ok := mappingValue(spec, "defaults"); ok && defaults.Kind == yaml.MappingNode {
		if repair, ok := mappingValue(defaults, "repair"); ok && repair.Kind == yaml.MappingNode {
			if actor, ok := mappingValue(repair, "actor"); ok {
				if err := rejectReservedActorScalar(actor, "defaults.repair.actor"); err != nil {
					return err
				}
			}
		}
		if phaseDefaults, ok := mappingValue(defaults, "phases"); ok && phaseDefaults.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(phaseDefaults.Content); i += 2 {
				kindNode, phaseDefault := phaseDefaults.Content[i], phaseDefaults.Content[i+1]
				if phaseDefault.Kind != yaml.MappingNode {
					continue
				}
				if actor, ok := mappingValue(phaseDefault, "actor"); ok {
					if err := rejectReservedActorScalar(actor, "defaults.phases."+kindNode.Value+".actor"); err != nil {
						return err
					}
				}
			}
		}
	}

	if validations, ok := mappingValue(spec, "validation"); ok && validations.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(validations.Content); i += 2 {
			nameNode, validation := validations.Content[i], validations.Content[i+1]
			if validation.Kind != yaml.MappingNode {
				continue
			}
			onFailure, ok := mappingValue(validation, "onFailure")
			if !ok || onFailure.Kind != yaml.MappingNode {
				continue
			}
			repair, ok := mappingValue(onFailure, "repair")
			if !ok || repair.Kind != yaml.MappingNode {
				continue
			}
			if actor, ok := mappingValue(repair, "actor"); ok {
				if err := rejectReservedActorScalar(actor, "validation."+nameNode.Value+".onFailure.repair.actor"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func rejectReservedActorScalar(actor *yaml.Node, path string) error {
	if value, ok := scalarValueFollowingAliases(actor); ok && strings.HasPrefix(value, inlineActorPrefix) {
		return fmt.Errorf("line %d: %s references reserved inline actor name %q", actor.Line, path, value)
	}
	return nil
}

// scalarValueFollowingAliases returns the scalar value represented by n,
// following yaml alias chains without changing the parsed document. Non-scalar
// targets return ok=false and are left to the canonical decoder to validate.
// Cyclic or broken alias chains also return ok=false; yaml.v3 will report those
// structural problems during normal decoding.
func scalarValueFollowingAliases(n *yaml.Node) (value string, ok bool) {
	seen := map[*yaml.Node]bool{}
	for n != nil && n.Kind == yaml.AliasNode {
		if seen[n] || n.Alias == nil {
			return "", false
		}
		seen[n] = true
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

func rejectMergeKey(mapping *yaml.Node, path string) error {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Value == "<<" || key.Tag == "!!merge" {
			return fmt.Errorf("line %d: YAML merge keys are not supported in %s when concise authoring syntax is used; write the canonical fields explicitly", key.Line, path)
		}
	}
	return nil
}

func documentMapping(root *yaml.Node) *yaml.Node {
	if root == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
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

// yaml.v3 v3.0.1 can change a folded scalar when a yaml.Node is marshaled and
// reparsed. Quote folded scalar values before marshaling so the already-parsed
// semantic value is serialized literally rather than folded a second time.
func marshalYAMLNodePreservingFoldedScalars(n *yaml.Node) ([]byte, error) {
	stable := cloneYAMLNode(n)
	quoteFoldedScalars(stable)
	return yaml.Marshal(stable)
}

func quoteFoldedScalars(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode && n.Style&yaml.FoldedStyle != 0 {
		n.Style = yaml.DoubleQuotedStyle
	}
	for _, child := range n.Content {
		quoteFoldedScalars(child)
	}
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
