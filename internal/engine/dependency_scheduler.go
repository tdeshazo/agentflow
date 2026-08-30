package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/internal/workspacepath"
)

// PhaseAcceptance reports whether a phase has the durable accepted-phase
// evidence required to satisfy a dependency. It deliberately has no actor or
// provider result in its contract.
type PhaseAcceptance func(phaseID string) (bool, error)

// ReadyNodeScheduler derives ready nodes from the immutable dependency graph
// and durable acceptance evidence. It owns no mutable scheduler bookkeeping,
// so a restarted scheduler makes the same decision from the same state. Its
// Ready method is also the future concurrent scheduler seam; v1alpha2 uses
// Next for deterministic serial execution.
type ReadyNodeScheduler struct {
	graph workflow.PhaseDependencyGraph
}

func NewReadyNodeScheduler(graph workflow.PhaseDependencyGraph) ReadyNodeScheduler {
	return ReadyNodeScheduler{graph: graph}
}

// Ready returns every currently ready, not-yet-accepted node in authored
// order. A node is ready exactly when all of its declared dependencies have
// accepted-phase evidence.
func (s ReadyNodeScheduler) Ready(accepted PhaseAcceptance) ([]workflow.PhaseDependencyNode, error) {
	ready := make([]workflow.PhaseDependencyNode, 0)
	for _, node := range s.graph.Nodes {
		complete, err := accepted(node.ID)
		if err != nil {
			return nil, fmt.Errorf("inspect phase %q acceptance: %w", node.ID, err)
		}
		if complete {
			continue
		}
		dependenciesReady := true
		for _, dependency := range s.graph.Dependencies(node.ID) {
			complete, err := accepted(dependency)
			if err != nil {
				return nil, fmt.Errorf("inspect dependency %q for phase %q: %w", dependency, node.ID, err)
			}
			if !complete {
				dependenciesReady = false
				break
			}
		}
		if dependenciesReady {
			ready = append(ready, node)
		}
	}
	return ready, nil
}

// Next returns the first ready node in authored order. It is intentionally a
// thin serial policy over Ready so future parallel dispatch can reuse the
// same readiness semantics without changing the language contract.
func (s ReadyNodeScheduler) Next(accepted PhaseAcceptance) (*workflow.PhaseDependencyNode, error) {
	ready, err := s.Ready(accepted)
	if err != nil || len(ready) == 0 {
		return nil, err
	}
	return &ready[0], nil
}

// AllAccepted reports whether every graph node has accepted-phase evidence.
// It lets the serial scheduler distinguish a genuinely completed graph from
// an invalid or blocked graph with no currently ready nodes.
func (s ReadyNodeScheduler) AllAccepted(accepted PhaseAcceptance) (bool, error) {
	for _, node := range s.graph.Nodes {
		complete, err := accepted(node.ID)
		if err != nil {
			return false, fmt.Errorf("inspect phase %q acceptance: %w", node.ID, err)
		}
		if !complete {
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) phaseDependencyAccepted(phaseID string) (bool, error) {
	p, err := e.phaseByID(phaseID)
	if err != nil {
		return false, err
	}
	accepted, _, err := e.validCommitMarker(e.phaseMarkerName(p))
	return accepted, err
}

func (e *Engine) runV1Alpha2Schedule(ctx context.Context) error {
	if err := validateV1Alpha2ScheduleContract(e.Workflow); err != nil {
		return err
	}
	scheduler := NewReadyNodeScheduler(e.Workflow.DependencyGraph)
	for {
		next, err := scheduler.Next(e.phaseDependencyAccepted)
		if err != nil {
			return err
		}
		if next == nil {
			complete, err := scheduler.AllAccepted(e.phaseDependencyAccepted)
			if err != nil {
				return err
			}
			if !complete {
				return fmt.Errorf("dependency scheduler has no ready phase without durable accepted dependency evidence")
			}
			if err := e.requireAllWorkItems(); err != nil {
				return err
			}
			for _, gate := range e.Workflow.Spec.HumanGates {
				if err := e.runHuman(ctx, gate.ID); err != nil {
					return err
				}
			}
			if err := e.requireCompletionEvidence(); err != nil {
				return err
			}
			return e.runCompletion(ctx, "default")
		}
		if err := e.runPhase(ctx, next.ID); err != nil {
			return err
		}
	}
}

// validateV1Alpha2ScheduleContract is the runtime fail-closed boundary for
// callers that construct or mutate a normalized Workflow directly. File-based
// callers normally pass through workflow.Validate first, but Decode and the
// Go API intentionally remain separate from semantic validation. No actor may
// run until the graph, references, and bounded acceptance policy are coherent.
func validateV1Alpha2ScheduleContract(w *workflow.Workflow) error {
	if w == nil {
		return fmt.Errorf("empty v1alpha2 workflow")
	}
	if len(w.Spec.Workspace.MutationPolicy.Allowed) == 0 {
		return fmt.Errorf("v1alpha2 workspace allowWrites must declare at least one path")
	}
	for i, path := range w.Spec.Workspace.MutationPolicy.Allowed {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("v1alpha2 workspace allowWrites[%d] must not be empty", i)
		}
		if _, ok := workspacepath.Clean(path); !ok {
			return fmt.Errorf("v1alpha2 workspace allowWrites[%d] must be workspace-relative", i)
		}
	}
	for _, rule := range w.Spec.Workspace.MutationPolicy.Integrity {
		for i, path := range rule.Paths {
			if _, ok := workspacepath.Clean(path); !ok {
				return fmt.Errorf("integrity rule %q path %d must be workspace-relative", rule.ID, i)
			}
		}
		for i, path := range rule.Exclude {
			if _, ok := workspacepath.Clean(path); !ok {
				return fmt.Errorf("integrity rule %q exclusion %d must be workspace-relative", rule.ID, i)
			}
		}
	}
	for name, artifact := range w.Spec.Contracts.Artifacts {
		for i, path := range artifact.Paths {
			if _, ok := workspacepath.Clean(path); !ok {
				return fmt.Errorf("artifact %q path %d must be workspace-relative", name, i)
			}
		}
	}
	if adapter := w.Spec.Criteria.MarkdownAdapter; adapter != nil {
		if _, ok := workspacepath.Clean(adapter.Path); !ok {
			return fmt.Errorf("Markdown checklist adapter path must be workspace-relative")
		}
	}

	phaseIndexes := make(map[string]int, len(w.Spec.Phases))
	for i, phase := range w.Spec.Phases {
		if phase.ID == "" {
			return fmt.Errorf("phase %d has empty id", i)
		}
		if _, exists := phaseIndexes[phase.ID]; exists {
			return fmt.Errorf("duplicate phase id %q", phase.ID)
		}
		phaseIndexes[phase.ID] = i
		if phase.Kind != "implementation" && phase.Kind != "audit" {
			return fmt.Errorf("phase %q has unsupported v1alpha2 kind %q", phase.ID, phase.Kind)
		}
		if phase.Actor == "" {
			return fmt.Errorf("phase %q has no actor", phase.ID)
		}
		if _, exists := w.Spec.Agents[phase.Actor]; !exists {
			return fmt.Errorf("phase %q references unknown actor %q", phase.ID, phase.Actor)
		}
		if phase.Validation == "" {
			return fmt.Errorf("phase %q has no deterministic validation", phase.ID)
		}
		if _, exists := w.Spec.Validation[phase.Validation]; !exists {
			return fmt.Errorf("phase %q references unknown validation %q", phase.ID, phase.Validation)
		}
	}
	agentNames := make([]string, 0, len(w.Spec.Agents))
	for name := range w.Spec.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)
	for _, name := range agentNames {
		agent := w.Spec.Agents[name]
		if name == "" {
			return fmt.Errorf("v1alpha2 agent name must not be empty")
		}
		if strings.TrimSpace(agent.Runner) == "" || strings.TrimSpace(agent.Model) == "" {
			return fmt.Errorf("agent %q requires runner and model", name)
		}
	}

	graph := w.DependencyGraph
	if len(graph.Nodes) != len(w.Spec.Phases) {
		return fmt.Errorf("dependency graph has %d nodes for %d phases", len(graph.Nodes), len(w.Spec.Phases))
	}
	for i, node := range graph.Nodes {
		if node.ID != w.Spec.Phases[i].ID || node.AuthoredOrder != i {
			return fmt.Errorf("dependency graph node %d does not match authored phase order", i)
		}
	}
	seenEdges := make(map[[2]string]bool, len(graph.Edges))
	adjacency := make(map[string][]string, len(graph.Nodes))
	for i, edge := range graph.Edges {
		if edge.SatisfiedWhen != workflow.PhaseDependencyAccepted {
			return fmt.Errorf("dependency edge %d does not require deterministic phase acceptance", i)
		}
		if _, exists := phaseIndexes[edge.Phase]; !exists {
			return fmt.Errorf("dependency edge %d references unknown phase %q", i, edge.Phase)
		}
		if _, exists := phaseIndexes[edge.DependsOn]; !exists {
			return fmt.Errorf("phase %q references unknown dependency %q", edge.Phase, edge.DependsOn)
		}
		if edge.Phase == edge.DependsOn {
			return fmt.Errorf("phase %q must not depend on itself", edge.Phase)
		}
		edgeKey := [2]string{edge.Phase, edge.DependsOn}
		if seenEdges[edgeKey] {
			return fmt.Errorf("duplicate dependency %q for phase %q", edge.DependsOn, edge.Phase)
		}
		seenEdges[edgeKey] = true
		adjacency[edge.Phase] = append(adjacency[edge.Phase], edge.DependsOn)
	}

	state := make(map[string]uint8, len(phaseIndexes))
	var visit func(string) error
	visit = func(id string) error {
		state[id] = 1
		for _, dependency := range adjacency[id] {
			switch state[dependency] {
			case 1:
				return fmt.Errorf("dependency cycle involving phase %q", dependency)
			case 0:
				if err := visit(dependency); err != nil {
					return err
				}
			}
		}
		state[id] = 2
		return nil
	}
	for _, phase := range w.Spec.Phases {
		if state[phase.ID] == 0 {
			if err := visit(phase.ID); err != nil {
				return err
			}
		}
	}

	completion, exists := w.Spec.Completion["default"]
	if !exists || completion.FinalValidation == "" {
		return fmt.Errorf("v1alpha2 completion requires a deterministic final validation")
	}
	if _, exists := w.Spec.Validation[completion.FinalValidation]; !exists {
		return fmt.Errorf("completion references unknown validation %q", completion.FinalValidation)
	}
	validationNames := make([]string, 0, len(w.Spec.Validation))
	for name := range w.Spec.Validation {
		validationNames = append(validationNames, name)
	}
	sort.Strings(validationNames)
	for _, name := range validationNames {
		validation := w.Spec.Validation[name]
		if len(validation.Steps) == 0 {
			return fmt.Errorf("validation %q has no deterministic steps", name)
		}
		for i, dependency := range validation.Dependencies {
			if _, ok := workspacepath.Clean(dependency); !ok {
				return fmt.Errorf("validation %q dependency %d must be workspace-relative", name, i)
			}
		}
		steps := append(append([]workflow.ToolUse{}, validation.Steps...), validation.OnFailure.Then...)
		for _, step := range steps {
			if step.Uses == "" {
				return fmt.Errorf("validation %q contains an empty deterministic tool reference", name)
			}
			tool, exists := w.Spec.Tools[step.Uses]
			if !exists {
				return fmt.Errorf("validation %q references unknown tool %q", name, step.Uses)
			}
			if tool.Type == "" {
				return fmt.Errorf("validation %q references tool %q without a type", name, step.Uses)
			}
			if tool.Type == "shell" && strings.TrimSpace(tool.Command) == "" {
				return fmt.Errorf("validation %q references shell tool %q with an empty command", name, step.Uses)
			}
		}
		if validation.OnFailure.Strategy == "" && validation.OnFailure.MaxRepairAttempts != 0 {
			return fmt.Errorf("validation %q declares a repair budget without repair-once", name)
		}
		if validation.OnFailure.Strategy == "repair-once" {
			if validation.OnFailure.MaxRepairAttempts != 1 {
				return fmt.Errorf("validation %q repair-once requires exactly one attempt", name)
			}
			if _, exists := w.Spec.Agents[validation.OnFailure.Repair.Actor]; !exists || validation.OnFailure.Repair.Actor == "" {
				return fmt.Errorf("validation %q references unknown repair actor %q", name, validation.OnFailure.Repair.Actor)
			}
		} else if validation.OnFailure.Strategy != "" {
			return fmt.Errorf("validation %q has unsupported repair strategy %q", name, validation.OnFailure.Strategy)
		}
	}
	return nil
}
