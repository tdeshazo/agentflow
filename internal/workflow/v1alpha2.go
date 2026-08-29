package workflow

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	v1alpha1APIVersion   = "agentflow.dev/v1alpha1"
	v1alpha2APIVersion   = "agentflow.dev/v1alpha2"
	v1alpha2RepairPrompt = `Diagnose and repair the deterministic validation failure below. Do not weaken or remove validation.

{{ validation.failure.log }}`
)

// V1Alpha2Workflow is the explicit concise authoring contract. It is not a
// second executable model: normalizeV1Alpha2 projects it into Workflow, which
// remains the shared runtime-facing representation.
type V1Alpha2Workflow struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   V1Alpha2Metadata `yaml:"metadata"`
	Spec       V1Alpha2Spec     `yaml:"spec"`
	File       string           `yaml:"-"`
}

type V1Alpha2Metadata struct {
	Name string `yaml:"name"`
}

type V1Alpha2Spec struct {
	Workspace  V1Alpha2Workspace             `yaml:"workspace"`
	Agents     map[string]V1Alpha2Agent      `yaml:"agents"`
	Validation map[string]V1Alpha2Validation `yaml:"validation"`
	Phases     []V1Alpha2Phase               `yaml:"phases"`
	Completion V1Alpha2Completion            `yaml:"completion"`
}

type V1Alpha2Workspace struct {
	AllowWrites []string `yaml:"allowWrites"`
}

type V1Alpha2Agent struct {
	Runner            string `yaml:"runner"`
	Model             string `yaml:"model"`
	Sandbox           string `yaml:"sandbox"`
	Approval          string `yaml:"approval"`
	Ephemeral         bool   `yaml:"ephemeral"`
	MayCommit         bool   `yaml:"may_commit"`
	OutputLastMessage bool   `yaml:"output_last_message"`
}

// UnmarshalYAML keeps named and inline v1alpha2 agents on exactly the same
// strict authoring schema.
func (a *V1Alpha2Agent) UnmarshalYAML(n *yaml.Node) error {
	resolved, err := resolveYAMLNode(n)
	if err != nil {
		return err
	}
	if resolved.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: agent must be a mapping", n.Line)
	}

	var out V1Alpha2Agent
	seen := map[string]bool{}
	for i := 0; i+1 < len(resolved.Content); i += 2 {
		keyNode := resolved.Content[i]
		key, ok := scalarValueFollowingAliases(keyNode)
		if !ok {
			return fmt.Errorf("line %d: agent field name must be a scalar", keyNode.Line)
		}
		if seen[key] {
			return fmt.Errorf("line %d: mapping key %q already defined", keyNode.Line, key)
		}
		seen[key] = true

		value, err := resolveYAMLNode(resolved.Content[i+1])
		if err != nil {
			return err
		}
		switch key {
		case "runner":
			out.Runner, err = v1Alpha2AgentString(value, key)
		case "model":
			out.Model, err = v1Alpha2AgentString(value, key)
		case "sandbox":
			out.Sandbox, err = v1Alpha2AgentString(value, key)
		case "approval":
			out.Approval, err = v1Alpha2AgentString(value, key)
		case "ephemeral":
			out.Ephemeral, err = v1Alpha2AgentBool(value, key)
		case "may_commit":
			out.MayCommit, err = v1Alpha2AgentBool(value, key)
		case "output_last_message":
			out.OutputLastMessage, err = v1Alpha2AgentBool(value, key)
		default:
			return fmt.Errorf("line %d: field %s not found in type workflow.V1Alpha2Agent", keyNode.Line, key)
		}
		if err != nil {
			return err
		}
	}
	*a = out
	return nil
}

func v1Alpha2AgentString(n *yaml.Node, field string) (string, error) {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", fmt.Errorf("line %d: cannot unmarshal %s into Go struct field V1Alpha2Agent.%s of type string", n.Line, n.Tag, field)
	}
	return n.Value, nil
}

func v1Alpha2AgentBool(n *yaml.Node, field string) (bool, error) {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
		return false, fmt.Errorf("line %d: cannot unmarshal %s into Go struct field V1Alpha2Agent.%s of type bool", n.Line, n.Tag, field)
	}
	var value bool
	if err := n.Decode(&value); err != nil {
		return false, err
	}
	return value, nil
}

type V1Alpha2Validation struct {
	Run     string               `yaml:"run"`
	Repair  V1Alpha2RepairPolicy `yaml:"repair"`
	present map[string]bool
}

type V1Alpha2RepairPolicy struct {
	Once    string `yaml:"once"`
	present map[string]bool
}

// UnmarshalYAML keeps the concise repair policy strict while retaining field
// presence. A zero-valued policy is meaningful when repair is omitted, but it
// is malformed when repair was explicitly declared without once: <actor>.
func (p *V1Alpha2RepairPolicy) UnmarshalYAML(n *yaml.Node) error {
	resolved, err := resolveYAMLNode(n)
	if err != nil {
		return err
	}
	if resolved.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: repair must be a mapping containing once: <actor>", n.Line)
	}

	var once string
	present := map[string]bool{}
	for i := 0; i+1 < len(resolved.Content); i += 2 {
		key, ok := scalarValueFollowingAliases(resolved.Content[i])
		if !ok {
			return fmt.Errorf("line %d: repair policy key must be a scalar", resolved.Content[i].Line)
		}
		if key != "once" {
			return fmt.Errorf("line %d: field %s not found in type workflow.V1Alpha2RepairPolicy", resolved.Content[i].Line, key)
		}
		if present[key] {
			return fmt.Errorf("line %d: mapping key %q already defined", resolved.Content[i].Line, key)
		}
		present[key] = true
		value, err := resolveYAMLNode(resolved.Content[i+1])
		if err != nil {
			return err
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return fmt.Errorf("line %d: cannot unmarshal %s into Go struct field V1Alpha2RepairPolicy.once of type string", resolved.Content[i+1].Line, value.Tag)
		}
		once = value.Value
	}
	*p = V1Alpha2RepairPolicy{Once: once, present: present}
	return nil
}

// UnmarshalYAML retains presence for validation.repair so an explicitly empty
// policy cannot be confused with an omitted policy. It also keeps the v1alpha2
// authoring contract strict even though this type has a custom decoder.
func (v *V1Alpha2Validation) UnmarshalYAML(n *yaml.Node) error {
	resolved, err := resolveYAMLNode(n)
	if err != nil {
		return err
	}
	if resolved.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: validation must be a mapping", n.Line)
	}

	var out V1Alpha2Validation
	present := map[string]bool{}
	for i := 0; i+1 < len(resolved.Content); i += 2 {
		key, ok := scalarValueFollowingAliases(resolved.Content[i])
		if !ok {
			return fmt.Errorf("line %d: validation field name must be a scalar", resolved.Content[i].Line)
		}
		if present[key] {
			return fmt.Errorf("line %d: mapping key %q already defined", resolved.Content[i].Line, key)
		}
		present[key] = true
		switch key {
		case "run":
			value, err := resolveYAMLNode(resolved.Content[i+1])
			if err != nil {
				return err
			}
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("line %d: cannot unmarshal %s into Go struct field V1Alpha2Validation.run of type string", resolved.Content[i+1].Line, value.Tag)
			}
			out.Run = value.Value
		case "repair":
			if err := resolved.Content[i+1].Decode(&out.Repair); err != nil {
				return err
			}
		default:
			return fmt.Errorf("line %d: field %s not found in type workflow.V1Alpha2Validation", resolved.Content[i].Line, key)
		}
	}
	out.present = present
	*v = out
	return nil
}

func (v V1Alpha2Validation) repairDeclared() bool {
	return v.present["repair"] || v.Repair.Once != ""
}

func (p V1Alpha2RepairPolicy) onceDeclared() bool {
	return p.present["once"] || p.Once != ""
}

// V1Alpha2Actor is an authoring-only scalar-or-inline-agent choice. Both forms
// lower to the shared Workflow agent map and scalar Phase.Actor reference.
type V1Alpha2Actor struct {
	Name   string
	Inline *V1Alpha2Agent
}

// UnmarshalYAML accepts either a named actor reference or the same mapping
// accepted by V1Alpha2Agent.
func (a *V1Alpha2Actor) UnmarshalYAML(n *yaml.Node) error {
	resolved, err := resolveYAMLNode(n)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case yaml.ScalarNode:
		if resolved.Tag != "!!str" {
			return fmt.Errorf("line %d: phase actor must be a named agent string or an inline agent mapping", n.Line)
		}
		*a = V1Alpha2Actor{Name: resolved.Value}
		return nil
	case yaml.MappingNode:
		var inline V1Alpha2Agent
		if err := decodeKnownNode(resolved, &inline); err != nil {
			return err
		}
		*a = V1Alpha2Actor{Inline: &inline}
		return nil
	default:
		return fmt.Errorf("line %d: phase actor must be a named agent string or an inline agent mapping", n.Line)
	}
}

type V1Alpha2Phase struct {
	ID         string        `yaml:"id"`
	Actor      V1Alpha2Actor `yaml:"actor"`
	Prompt     string        `yaml:"prompt"`
	Validation string        `yaml:"validation"`
	DependsOn  []string      `yaml:"dependsOn"`
}

type V1Alpha2Completion struct {
	Validation string `yaml:"validation"`
}

func normalizeV1Alpha2(authored *V1Alpha2Workflow, locations Locations) (*Document, error) {
	if authored == nil {
		return nil, fmt.Errorf("empty v1alpha2 workflow")
	}
	if err := rejectV1Alpha2ReservedActorNamespace(authored, locations); err != nil {
		return nil, err
	}
	w := &Workflow{
		APIVersion: authored.APIVersion,
		Kind:       authored.Kind,
		Metadata: Metadata{
			Name: authored.Metadata.Name,
		},
		Spec: Spec{
			Workspace: WorkspaceSpec{MutationPolicy: MutationPolicy{
				Allowed: append([]string(nil), authored.Spec.Workspace.AllowWrites...),
			}},
			// v1alpha2 deliberately has no procedural lifecycle fields. Lower the
			// normal safe path to an explicit shared policy so the executable
			// representation and expanded plan cannot hide that authority.
			Lifecycle:  LifecyclePolicy{Policy: "safe-resume"},
			Agents:     make(map[string]Agent, len(authored.Spec.Agents)),
			Tools:      make(map[string]Tool, len(authored.Spec.Validation)),
			Validation: make(map[string]Validation, len(authored.Spec.Validation)),
			Phases:     make([]Phase, 0, len(authored.Spec.Phases)),
			Completion: map[string]Completion{"default": {FinalValidation: authored.Spec.Completion.Validation}},
		},
		File: authored.File,
	}
	for name, agent := range authored.Spec.Agents {
		w.Spec.Agents[name] = normalizeV1Alpha2Agent(agent)
	}
	for name, validation := range authored.Spec.Validation {
		toolName := v1Alpha2ValidationToolName(name)
		w.Spec.Tools[toolName] = Tool{Type: "shell", Command: validation.Run}
		v := Validation{Steps: []ToolUse{{Uses: toolName}}}
		if validation.repairDeclared() {
			if !validation.Repair.onceDeclared() || strings.TrimSpace(validation.Repair.Once) == "" {
				path := "spec.validation." + name + ".repair.once"
				return nil, v1Alpha2SourceError(locations, path, fmt.Errorf("validation %q repair.once is required", name))
			}
			v.OnFailure = FailurePolicy{
				Strategy:          "repair-once",
				MaxRepairAttempts: 1,
				Repair: Repair{
					Actor:  validation.Repair.Once,
					Prompt: v1alpha2RepairPrompt,
				},
			}
		}
		w.Spec.Validation[name] = v
	}
	graph := buildV1Alpha2PhaseDependencyGraph(authored.Spec.Phases)
	for i, phase := range authored.Spec.Phases {
		actorName := phase.Actor.Name
		if phase.Actor.Inline != nil {
			phaseKey := fmt.Sprintf("phase_%d", i)
			if strings.TrimSpace(phase.ID) != "" {
				phaseKey = phase.ID
			}
			actorName = inlineActorPrefix + phaseKey
			if _, exists := w.Spec.Agents[actorName]; exists {
				path := fmt.Sprintf("spec.phases[%d].actor", i)
				return nil, v1Alpha2SourceError(locations, path, fmt.Errorf("inline actor for phase %q conflicts with generated agent name %q", phaseKey, actorName))
			}
			w.Spec.Agents[actorName] = normalizeV1Alpha2Agent(*phase.Actor.Inline)
		}
		w.Spec.Phases = append(w.Spec.Phases, Phase{
			ID: phase.ID, Kind: "implementation", Actor: actorName,
			Prompt: phase.Prompt, Validation: phase.Validation,
		})
	}
	w.DependencyGraph = clonePhaseDependencyGraph(graph)
	return &Document{
		Workflow:          w,
		Locations:         locations,
		DependencyGraph:   graph,
		PhaseDependencies: graph.phaseDependenciesMap(),
	}, nil
}

func normalizeV1Alpha2Agent(agent V1Alpha2Agent) Agent {
	return Agent{
		Runner:            agent.Runner,
		Model:             agent.Model,
		Sandbox:           agent.Sandbox,
		Approval:          agent.Approval,
		Ephemeral:         agent.Ephemeral,
		MayCommit:         agent.MayCommit,
		OutputLastMessage: agent.OutputLastMessage,
	}
}

func rejectV1Alpha2ReservedActorNamespace(authored *V1Alpha2Workflow, locations Locations) error {
	for _, name := range sortedKeys(authored.Spec.Agents) {
		if strings.HasPrefix(name, inlineActorPrefix) {
			path := "spec.agents." + name
			return v1Alpha2SourceError(locations, path, fmt.Errorf("agent name %q uses reserved prefix %q", name, inlineActorPrefix))
		}
	}
	for _, name := range sortedKeys(authored.Spec.Validation) {
		repair := authored.Spec.Validation[name].Repair
		if strings.HasPrefix(repair.Once, inlineActorPrefix) {
			path := "spec.validation." + name + ".repair.once"
			return v1Alpha2SourceError(locations, path, fmt.Errorf("validation.%s.repair.once references reserved inline actor name %q", name, repair.Once))
		}
	}
	for i, phase := range authored.Spec.Phases {
		if phase.Actor.Inline == nil && strings.HasPrefix(phase.Actor.Name, inlineActorPrefix) {
			path := fmt.Sprintf("spec.phases[%d].actor", i)
			return v1Alpha2SourceError(locations, path, fmt.Errorf("phases[%d].actor references reserved inline actor name %q", i, phase.Actor.Name))
		}
	}
	return nil
}

func v1Alpha2SourceError(locations Locations, path string, err error) error {
	position := locations[path]
	if position.Line == 0 {
		for parent := path; parent != ""; {
			i := strings.LastIndexAny(parent, ".[")
			if i < 0 {
				break
			}
			parent = parent[:i]
			if position = locations[parent]; position.Line != 0 {
				break
			}
		}
	}
	return &sourceDiagnosticError{path: path, position: position, err: err}
}

func v1Alpha2ValidationToolName(name string) string {
	return "__v1alpha2_validation__" + name
}

func rejectV1Alpha2MergeKeys(root *yaml.Node) error {
	doc := documentMapping(root)
	if doc == nil {
		return nil
	}
	spec, ok := mappingValue(doc, "spec")
	if !ok {
		return nil
	}
	seen := map[*yaml.Node]bool{}
	var walk func(*yaml.Node, string) error
	walk = func(n *yaml.Node, path string) error {
		if n == nil {
			return nil
		}
		resolved, err := resolveYAMLNode(n)
		if err != nil {
			return err
		}
		if resolved != n {
			if seen[resolved] {
				return nil
			}
			seen[resolved] = true
		}
		n = resolved
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, ok := scalarValueFollowingAliases(n.Content[i])
				if (ok && key == "<<") || n.Content[i].Tag == "!!merge" {
					return fmt.Errorf("line %d: YAML merge keys are not supported in %s; write the canonical fields explicitly", n.Content[i].Line, path)
				}
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i].Value
				child := key
				if path != "" {
					child = path + "." + key
				}
				if err := walk(n.Content[i+1], child); err != nil {
					return err
				}
			}
			return nil
		}
		if n.Kind == yaml.SequenceNode {
			for i, child := range n.Content {
				if err := walk(child, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(spec, "spec")
}

func validateV1Alpha2(d *Document) Result {
	r := Result{Status: Executable, Document: d}
	if d == nil || d.V1Alpha2 == nil {
		return Result{Status: Invalid, Document: d, Diagnostics: []Diagnostic{{Status: Invalid, Message: "empty v1alpha2 workflow document"}}}
	}
	v := v1alpha2Validator{result: &r, locations: d.Locations, w: d.V1Alpha2}
	v.roots()
	v.references()
	v.runtimeSurface()
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Status == Invalid {
			r.Status = Invalid
			return r
		}
	}
	normalized, err := NormalizeWorkflow(d)
	if err != nil {
		r.Status = Invalid
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Status: Invalid, Message: "normalize workflow: " + err.Error()})
		return r
	}
	r.Normalized = normalized
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Status == Unsupported {
			r.Status = Unsupported
			break
		}
	}
	return r
}

type v1alpha2Validator struct {
	result    *Result
	locations Locations
	w         *V1Alpha2Workflow
}

func (v v1alpha2Validator) add(path, format string, args ...any) {
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{
		Status: Invalid, Path: path, Position: v.location(path), Message: fmt.Sprintf(format, args...),
	})
}

func (v v1alpha2Validator) addUnsupported(path, format string, args ...any) {
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{
		Status: Unsupported, Path: path, Position: v.location(path), Message: fmt.Sprintf(format, args...),
	})
}

func (v v1alpha2Validator) location(path string) Position {
	for path != "" {
		if p, ok := v.locations[path]; ok {
			return p
		}
		i := strings.LastIndexAny(path, ".[")
		if i < 0 {
			break
		}
		path = path[:i]
	}
	return Position{}
}

func (v v1alpha2Validator) roots() {
	if v.w.APIVersion != v1alpha2APIVersion {
		v.add("apiVersion", "must be %s (got %q)", v1alpha2APIVersion, v.w.APIVersion)
	}
	if v.w.Kind != "AgentWorkflow" {
		v.add("kind", "must be AgentWorkflow (got %q)", v.w.Kind)
	}
	if v.w.Metadata.Name == "" {
		v.add("metadata.name", "is required")
	}
	if len(v.w.Spec.Workspace.AllowWrites) == 0 {
		v.add("spec.workspace.allowWrites", "must declare at least one workspace-relative path")
	}
	for i, path := range v.w.Spec.Workspace.AllowWrites {
		if strings.TrimSpace(path) == "" {
			v.add(fmt.Sprintf("spec.workspace.allowWrites[%d]", i), "must not be empty")
		}
		if filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "../") || strings.HasPrefix(path, `..\\`) || path == ".." {
			v.add(fmt.Sprintf("spec.workspace.allowWrites[%d]", i), "must be workspace-relative")
		}
	}
	for _, name := range sortedKeys(v.w.Spec.Agents) {
		a := v.w.Spec.Agents[name]
		v.identifier("spec.agents", "agent", name)
		if a.Runner == "" {
			v.add("spec.agents."+name+".runner", "is required")
		}
		if a.Model == "" {
			v.add("spec.agents."+name+".model", "is required")
		}
	}
	for _, name := range sortedKeys(v.w.Spec.Validation) {
		validation := v.w.Spec.Validation[name]
		v.identifier("spec.validation", "validation", name)
		if strings.TrimSpace(validation.Run) == "" {
			v.add("spec.validation."+name+".run", "is required")
		}
		if validation.repairDeclared() {
			path := "spec.validation." + name + ".repair.once"
			if !validation.Repair.onceDeclared() || strings.TrimSpace(validation.Repair.Once) == "" {
				v.add(path, "is required when repair is declared")
			} else {
				v.agent(path, validation.Repair.Once)
			}
		}
	}
}

func (v v1alpha2Validator) references() {
	graph := buildV1Alpha2PhaseDependencyGraph(v.w.Spec.Phases)
	phaseIndex := graph.phaseIndex()
	for i, phase := range v.w.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", i)
		if phase.ID == "" {
			v.add(path+".id", "is required")
		} else if !identifierPattern.MatchString(phase.ID) {
			v.add(path+".id", "phase id %q must match %s", phase.ID, identifierPattern.String())
		} else if phaseIndex[phase.ID] != i {
			v.add(path+".id", "duplicate phase id %q", phase.ID)
		}
		if phase.Actor.Inline != nil {
			v.agentFields(path+".actor", *phase.Actor.Inline)
		} else if phase.Actor.Name == "" {
			v.add(path+".actor", "is required")
		} else {
			v.agent(path+".actor", phase.Actor.Name)
		}
		if strings.TrimSpace(phase.Prompt) == "" {
			v.add(path+".prompt", "is required")
		}
		if phase.Validation == "" {
			v.add(path+".validation", "is required")
		} else {
			v.validation(path+".validation", phase.Validation)
		}
	}
	for i, phase := range v.w.Spec.Phases {
		seen := map[string]bool{}
		for _, edge := range graph.edgesForPhase(i) {
			dependency := edge.DependsOn
			j := edge.dependencyIndex
			path := fmt.Sprintf("spec.phases[%d].dependsOn[%d]", i, j)
			if dependency == "" {
				v.add(path, "must not be empty")
				continue
			}
			if seen[dependency] {
				v.add(path, "duplicate dependency %q", dependency)
			}
			seen[dependency] = true
			if dependency == phase.ID {
				v.add(path, "must not depend on itself")
			} else if _, ok := phaseIndex[dependency]; !ok {
				v.add(path, "unknown phase dependency %q", dependency)
			}
		}
	}
	if v.w.Spec.Completion.Validation == "" {
		v.add("spec.completion.validation", "is required")
	} else {
		v.validation("spec.completion.validation", v.w.Spec.Completion.Validation)
	}
	v.dependencyCycles(graph, phaseIndex)
}

func (v v1alpha2Validator) identifier(path, kind, id string) bool {
	if id == "" {
		v.add(path, "%s name must not be empty", kind)
		return false
	}
	if !identifierPattern.MatchString(id) {
		v.add(path+"."+id, "%s name %q must match %s", kind, id, identifierPattern.String())
		return false
	}
	return true
}

func (v v1alpha2Validator) agent(path, name string) {
	if _, ok := v.w.Spec.Agents[name]; !ok {
		v.add(path, "unknown agent %q", name)
	}
}

func (v v1alpha2Validator) agentFields(path string, agent V1Alpha2Agent) {
	if agent.Runner == "" {
		v.add(path+".runner", "is required")
	}
	if agent.Model == "" {
		v.add(path+".model", "is required")
	}
}

func (v v1alpha2Validator) runtimeSurface() {
	for _, name := range sortedKeys(v.w.Spec.Agents) {
		v.agentRuntimeSurface("spec.agents."+name, v.w.Spec.Agents[name])
	}
	for i, phase := range v.w.Spec.Phases {
		if phase.Actor.Inline != nil {
			v.agentRuntimeSurface(fmt.Sprintf("spec.phases[%d].actor", i), *phase.Actor.Inline)
		}
	}
}

func (v v1alpha2Validator) agentRuntimeSurface(path string, agent V1Alpha2Agent) {
	// v1alpha2 keeps runner provider-neutral so injected Go providers can use
	// custom runner names. The built-in Codex adapter is the only provider
	// whose approval policy is known during document validation.
	if agent.Runner == "codex" && agent.Approval != "" && agent.Approval != "never" {
		v.addUnsupported(path+".approval", "approval policy %q is not implemented", agent.Approval)
	}
}

func (v v1alpha2Validator) validation(path, name string) {
	if _, ok := v.w.Spec.Validation[name]; !ok {
		v.add(path, "unknown validation %q", name)
	}
}

func (v v1alpha2Validator) dependencyCycles(graph PhaseDependencyGraph, phaseIndex map[string]int) {
	state := make([]uint8, len(graph.Nodes))
	stack := make([]int, 0, len(graph.Nodes))
	stackPosition := make(map[int]int, len(graph.Nodes))
	var visit func(int)
	visit = func(index int) {
		state[index] = 1
		stackPosition[index] = len(stack)
		stack = append(stack, index)
		for _, edge := range graph.edgesForPhase(index) {
			dependencyIndex, exists := phaseIndex[edge.DependsOn]
			if !exists || dependencyIndex == index {
				continue
			}
			switch state[dependencyIndex] {
			case 0:
				visit(dependencyIndex)
			case 1:
				cycle := make([]string, 0, len(stack)-stackPosition[dependencyIndex]+1)
				for _, member := range stack[stackPosition[dependencyIndex]:] {
					cycle = append(cycle, graph.Nodes[member].ID)
				}
				cycle = append(cycle, graph.Nodes[dependencyIndex].ID)
				v.add(
					fmt.Sprintf("spec.phases[%d].dependsOn", dependencyIndex),
					"dependency cycle: %s",
					strings.Join(cycle, " -> "),
				)
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackPosition, index)
		state[index] = 2
	}
	for index := range graph.Nodes {
		if state[index] == 0 {
			visit(index)
		}
	}
}
