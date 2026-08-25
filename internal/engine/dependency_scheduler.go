package engine

import (
	"context"
	"fmt"

	"github.com/tdeshazo/agentflow/internal/workflow"
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
			return e.runCompletion(ctx, "default")
		}
		if err := e.runPhase(ctx, next.ID); err != nil {
			return err
		}
	}
}
