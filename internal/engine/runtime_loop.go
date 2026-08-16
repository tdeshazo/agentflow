package engine

import (
	"context"
	"fmt"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

func (e *Engine) runLoop(ctx context.Context, loop workflow.Loop) error {
	max, err := e.integer(nil, loop.MaxIterations)
	if err != nil {
		return fmt.Errorf("loop maxIterations: %w", err)
	}
	if max <= 0 {
		return fmt.Errorf("loop maxIterations must be positive")
	}
	for iteration := 0; ; iteration++ {
		condition, err := e.bool(nil, loop.While)
		if err != nil {
			return fmt.Errorf("loop condition: %w", err)
		}
		if !condition {
			return nil
		}
		if iteration >= max {
			return fmt.Errorf("loop exceeded maxIterations=%d without satisfying its condition", max)
		}
		progress, err := e.progressSnapshot()
		if err != nil {
			return err
		}
		criterion := progress.NextUnchecked()
		if criterion == "" {
			return fmt.Errorf("loop selected next unchecked criterion but none is available")
		}
		phaseID, ok := loop.DispatchByCriterion[criterion]
		if !ok {
			return fmt.Errorf("loop has no phase for next unchecked criterion %q", criterion)
		}
		phase, err := e.phaseByID(phaseID)
		if err != nil {
			return err
		}
		phaseCriterion, err := e.criterionText(phase.Criterion)
		if err != nil {
			return err
		}
		if phase.Kind != "criterion" || phaseCriterion != criterion {
			return fmt.Errorf("loop dispatch for %q points to phase %q, which does not target that criterion", criterion, phaseID)
		}
		before := progress.UncheckedCount
		if err := e.runPhase(ctx, phaseID); err != nil {
			return err
		}
		after, err := e.uncheckedCount()
		if err != nil {
			return err
		}
		delta := loop.RequireUncheckedCountDelta
		if after != before+delta {
			return fmt.Errorf("loop progress violation for %q: before=%d after=%d expected=%d", criterion, before, after, before+delta)
		}
	}
}
