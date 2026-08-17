package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

func (e *Engine) newActivePhase(id string) (ActivePhase, error) {
	start, err := e.Repo.Head()
	if err != nil {
		return ActivePhase{}, err
	}
	progress, err := e.progressSnapshot()
	if err != nil {
		return ActivePhase{}, err
	}
	return ActivePhase{PhaseID: id, StartCommit: start, UncheckedBefore: progress.UncheckedCount, CheckedBefore: progress.CheckedTexts()}, nil
}

func (e *Engine) runtimePhaseActions(p *workflow.Phase) ([]workflow.PhaseAction, error) {
	validation := e.phaseValidation(p)
	if validation == "" {
		return nil, fmt.Errorf("phase %s has no deterministic validation in its runtime lifecycle policy", p.ID)
	}
	actions := []workflow.PhaseAction{{Validate: validation}}
	if p.Kind == "criterion" {
		actions = append(actions, workflow.PhaseAction{AssertProgressIfApplicable: true})
	}
	if p.RequiresChange {
		actions = append(actions, workflow.PhaseAction{AssertNetRepositoryChangeSincePhaseStart: true})
	}
	if checkpoint := e.phaseCheckpoint(p); checkpoint != "" {
		actions = append(actions, workflow.PhaseAction{Checkpoint: checkpoint})
	}
	actions = append(actions,
		workflow.PhaseAction{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
		workflow.PhaseAction{ClearActivePhase: true},
	)
	return actions, nil
}

func (e *Engine) runPhaseActions(ctx context.Context, phase *workflow.Phase, active *ActivePhase, actions []workflow.PhaseAction) error {
	for _, action := range actions {
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(phase)); err != nil {
			var safetyErr *safetyViolation
			if errors.As(err, &safetyErr) {
				if persistErr := e.persistSafetyFailure(phase, err); persistErr != nil {
					return persistErr
				}
			}
			return err
		}
		if action.If != "" {
			ok, err := e.bool(phase, action.If)
			if err != nil {
				return fmt.Errorf("phase action condition: %w", err)
			}
			if !ok {
				continue
			}
		}
		if action.RequireCleanImplementationWorkspace {
			dirty, err := e.implementationDirtyFiles()
			if err != nil {
				return err
			}
			if len(dirty) != 0 {
				return fmt.Errorf("implementation workspace is dirty: %v", dirty)
			}
		}
		if action.CapturePhaseStartCommit {
			start, err := e.Repo.Head()
			if err != nil {
				return err
			}
			active.StartCommit = start
		}
		if action.CaptureUncheckedCountBefore {
			progress, err := e.progressSnapshot()
			if err != nil {
				return err
			}
			active.UncheckedBefore = progress.UncheckedCount
			active.CheckedBefore = progress.CheckedTexts()
		}
		if len(action.PersistActivePhase.Fields) > 0 {
			if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
				return err
			}
		}
		if action.Validate != "" {
			if err := e.runValidation(ctx, action.Validate, phase); err != nil {
				var safetyErr *safetyViolation
				if errors.As(err, &safetyErr) {
					return err
				}
				return &phaseValidationFailure{err: err}
			}
			active.ValidationPassed = true
		}
		if action.AssertProgress != nil && action.AssertProgress.Enabled {
			if err := e.assertProgressAction(phase, *active, *action.AssertProgress); err != nil {
				return err
			}
		}
		if action.AssertProgressIfApplicable && phase.Kind == "criterion" {
			if err := e.assertProgress(phase, *active); err != nil {
				return err
			}
		}
		if action.Checkpoint != "" {
			if err := e.assertAgentCommitPolicy(phase, *active); err != nil {
				return err
			}
			active.CheckpointPending = true
			if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
				return err
			}
			if err := e.runTool(ctx, action.Checkpoint, phase); err != nil {
				return err
			}
			head, err := e.Repo.Head()
			if err != nil {
				return err
			}
			active.CheckpointCommit = head
			active.CheckpointPending = false
			if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
				return err
			}
		}
		if action.AssertNetRepositoryChangeSincePhaseStart {
			if err := e.assertNetChange(phase, *active); err != nil {
				return err
			}
		}
		if action.MarkPhaseComplete != nil || action.MarkPhaseCompleteFlag {
			if !active.ValidationPassed {
				return fmt.Errorf("phase %s did not run a successful deterministic validation before acceptance", phase.ID)
			}
			if active.CheckpointCommit == "" {
				// A marker is acceptance evidence, not a progress hint. Compact
				// lifecycle declarations may omit an explicit checkpoint, but they
				// still receive the same durable checkpoint barrier before a phase
				// can become complete.
				if err := e.assertAgentCommitPolicy(phase, *active); err != nil {
					return err
				}
				active.CheckpointPending = true
				if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
					return err
				}
				if err := e.checkpoint(phase.Label, phase); err != nil {
					return err
				}
				head, err := e.Repo.Head()
				if err != nil {
					return err
				}
				active.CheckpointCommit = head
				active.CheckpointPending = false
				if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
					return err
				}
			}
			if err := e.markPhaseComplete(phase); err != nil {
				return err
			}
		}
		if action.ClearActivePhase {
			if err := e.Store.Delete(e.activeRecord()); err != nil {
				return err
			}
		}
		if action.Return != "" {
			return nil
		}
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(phase)); err != nil {
			var safetyErr *safetyViolation
			if errors.As(err, &safetyErr) {
				if persistErr := e.persistSafetyFailure(phase, err); persistErr != nil {
					return persistErr
				}
			}
			return err
		}
	}
	return nil
}

func (e *Engine) assertNetChange(phase *workflow.Phase, active ActivePhase) error {
	changed, err := e.Repo.HasNetChange(active.StartCommit)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("phase %s (%s) produced no net repository change", phase.ID, phase.Label)
	}
	return nil
}

func (e *Engine) assertAgentCommitPolicy(phase *workflow.Phase, active ActivePhase) error {
	agent, ok := e.Workflow.Spec.Agents[phase.Actor]
	if !ok {
		return fmt.Errorf("unknown actor %q", phase.Actor)
	}
	allowed := agent.MayCommit || e.Workflow.Spec.Workspace.AgentCommits.Allowed || e.Workflow.Spec.Workspace.Checkpointing.AgentCommitsAllowed
	if allowed {
		return nil
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if active.CheckpointPending {
		return nil
	}
	if active.CheckpointCommit != "" {
		if !e.Repo.ObjectExists(active.CheckpointCommit+"^{commit}") || !e.Repo.IsAncestor(active.CheckpointCommit, "HEAD") {
			return fmt.Errorf("phase %s checkpoint no longer descends from its recorded checkpoint %s", phase.ID, active.CheckpointCommit)
		}
		if head == active.CheckpointCommit {
			return nil
		}
	}
	if head != active.StartCommit {
		return fmt.Errorf("phase %s created commits but actor %q is not allowed to commit", phase.ID, phase.Actor)
	}
	return nil
}

func (e *Engine) markPhaseComplete(phase *workflow.Phase) error {
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	return e.Store.SetCommit(e.phaseMarkerName(phase), head)
}

func (e *Engine) requirePhaseCompletion(phase *workflow.Phase) error {
	if err := e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(phase)); err != nil {
		return fmt.Errorf("phase %s failed its final safety boundary: %w", phase.ID, err)
	}
	ok, head, err := e.validCommitMarker(e.phaseMarkerName(phase))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("phase %s finished without markPhaseComplete", phase.ID)
	}
	if _, active, err := e.Store.Resolve(e.activeRecord()); err != nil {
		return err
	} else if active {
		return fmt.Errorf("phase %s finished without clearActivePhase", phase.ID)
	}
	fmt.Fprintf(e.Out, "==> Phase %s complete at %s\n", phase.ID, shortSHA(head))
	return nil
}

func (e *Engine) phaseMarkerName(phase *workflow.Phase) string {
	pattern := e.Workflow.Spec.State.Records.CompletedPhasePattern
	if pattern == "" {
		if prefix := e.Workflow.Spec.State.Records.CompletedPhases; prefix != "" {
			return strings.TrimSuffix(prefix, "/") + "/" + phase.ID
		}
		return "phases/" + phase.ID
	}
	name, err := e.context(phase).Expand(pattern)
	if err != nil || name == "" {
		return "phases/" + phase.ID
	}
	return name
}
