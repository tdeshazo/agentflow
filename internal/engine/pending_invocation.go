package engine

import (
	"errors"
	"fmt"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

// invocationStateAvailable keeps low-level provider-boundary unit tests
// provider-neutral. Every constructed runtime Engine has a namespaced store,
// and therefore always takes the durable path below.
func (e *Engine) invocationStateAvailable() bool {
	return e.Store.Repo.Root != "" && e.Store.Namespace != ""
}

func (e *Engine) persistPendingInvocation(pending PendingActorInvocation) error {
	var existing PendingActorInvocation
	if ok, err := e.Store.GetJSON(e.pendingInvocationRecord(), &existing); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("pending actor invocation for %q must be reconciled before invoking %q", existing.Actor, pending.Actor)
	}
	if err := e.Store.SetJSON(e.pendingInvocationRecord(), pending); err != nil {
		return fmt.Errorf("persist pending actor invocation for %q: %w", pending.Actor, err)
	}
	return nil
}

// reconcilePendingInvocation is the crash-recovery half of the provider
// boundary. It writes attribution and any safety outcome before removing the
// pending record, so a crash at every boundary repeats only idempotent state
// writes and never changes pending execution into acceptance evidence.
func (e *Engine) reconcilePendingInvocation() (bool, error) {
	if !e.invocationStateAvailable() {
		return false, nil
	}
	var pending PendingActorInvocation
	ok, err := e.Store.GetJSON(e.pendingInvocationRecord(), &pending)
	if err != nil || !ok {
		return false, err
	}
	if pending.Version != legacyPendingActorInvocationVersion && pending.Version != pendingActorInvocationVersion {
		return false, fmt.Errorf("unsupported pending actor invocation version %d", pending.Version)
	}
	if pending.Actor == "" || pending.StartCommit == "" || pending.Role == "" {
		return false, errors.New("pending actor invocation is incomplete")
	}

	agent, knownActor := e.Workflow.Spec.Agents[pending.Actor]
	if !knownActor {
		return false, fmt.Errorf("pending actor invocation references unknown actor %q", pending.Actor)
	}
	head := ""
	moved := false
	authorized := true
	imported := false
	changed := []string{}
	var quarantine *gitstate.ActorWorktree
	var violation *safetyViolation
	if pending.Version == pendingActorInvocationVersion {
		result, quarantineErr := e.reconcileActorQuarantine(pending, agent)
		head = result.commit
		moved = result.moved
		authorized = result.authorized
		imported = result.imported
		changed = append(changed, result.changed...)
		quarantine = result.worktree
		if quarantineErr != nil {
			if !errors.As(quarantineErr, &violation) {
				return moved, quarantineErr
			}
		}
	} else {
		// Version 1 actors ran directly in the authoritative workspace. Ignore
		// fields unknown to that schema so its recovery behavior remains exactly
		// the HEAD-based reconciliation implemented by the binaries that wrote it.
		var err error
		head, err = e.Repo.Head()
		if err != nil {
			return false, fmt.Errorf("inspect repository HEAD while reconciling actor %q: %w", pending.Actor, err)
		}
		moved = head != pending.StartCommit
		if moved {
			authorized = e.effectiveActorCommitPermission(agent)
			if !authorized {
				violation = &safetyViolation{
					err:    fmt.Errorf("repository policy: actor %q moved HEAD but effective actor commit permission is false (may_commit is false and no workflow actor-commit permission is enabled)", pending.Actor),
					actor:  pending.Actor,
					commit: head,
				}
			}
		}
	}
	if moved && pending.PhaseID != "" {
		phase, err := e.phaseByID(pending.PhaseID)
		if err != nil {
			return true, fmt.Errorf("resolve pending invocation phase %q: %w", pending.PhaseID, err)
		}
		if err := e.recordPhaseCommitActor(phase, pending.Actor); err != nil {
			return true, err
		}
	}

	if err := e.Store.SetJSON(e.invocationOutcomeRecord(), ActorInvocationOutcome{
		PendingActorInvocation: pending,
		Commit:                 head,
		HeadMoved:              moved,
		Authorized:             authorized,
		Imported:               imported,
		ChangedPaths:           changed,
	}); err != nil {
		return moved, fmt.Errorf("persist actor invocation outcome for %q: %w", pending.Actor, err)
	}
	if violation != nil {
		if err := e.persistPendingInvocationSafety(pending, violation); err != nil {
			return moved, err
		}
	}
	if err := e.runInterruptionHook(interruptionAfterAuthority, pending); err != nil {
		return moved, err
	}
	if violation == nil && quarantine != nil {
		if err := quarantine.Remove(); err != nil {
			return moved, fmt.Errorf("remove compliant actor quarantine: %w", err)
		}
	}
	if err := e.Store.Delete(e.pendingInvocationRecord()); err != nil {
		return moved, fmt.Errorf("clear pending actor invocation for %q: %w", pending.Actor, err)
	}
	if violation != nil {
		return moved, violation
	}
	return moved, nil
}

func (e *Engine) runInterruptionHook(point interruptionPoint, pending PendingActorInvocation) error {
	if e.interruptionHook == nil {
		return nil
	}
	return e.interruptionHook(point, pending)
}

func (e *Engine) persistPendingInvocationSafety(pending PendingActorInvocation, violation *safetyViolation) error {
	if pending.PhaseID != "" {
		phase, err := e.phaseByID(pending.PhaseID)
		if err != nil {
			return err
		}
		if err := e.persistSafetyFailure(phase, violation); err != nil {
			return fmt.Errorf("persist pending actor safety failure: %w", err)
		}
		return nil
	}
	if pending.ValidationScope == "" {
		return nil
	}
	record := e.standaloneFailureRecordForScope(pending.ValidationScope)
	var prior validationFailureEvidence
	if ok, err := e.Store.GetJSON(record, &prior); err != nil {
		return err
	} else if ok && prior.FailureKind == PhaseFailureSafety {
		return nil
	}
	if err := e.Store.SetJSON(record, validationFailureEvidence{
		Validation:         pending.ValidationScope,
		FailureKind:        PhaseFailureSafety,
		Output:             errorOutput(violation),
		Actor:              violation.actor,
		Commit:             violation.commit,
		QuarantinePath:     violation.quarantine,
		IntegrityViolation: violation.integrityViolation,
	}); err != nil {
		return fmt.Errorf("persist pending actor safety failure: %w", err)
	}
	return nil
}

func (e *Engine) validationInvocationScope(validation string, phase *workflow.Phase) string {
	if phase != nil {
		return "phase/" + phase.ID + "/" + validation
	}
	return e.standaloneValidationScope(validation)
}
