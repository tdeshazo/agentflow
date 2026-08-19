package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tdeshazo/agentflow-spec/internal/clioutput"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

// phaseValidationFailure distinguishes an unsuccessful deterministic gate from
// a safety failure such as an out-of-scope mutation. Recovery may ask the same
// phase actor to continue after the former, but it must not paper over the
// latter by running more agent work.
type phaseValidationFailure struct{ err error }

func (e *phaseValidationFailure) Error() string { return e.err.Error() }
func (e *phaseValidationFailure) Unwrap() error { return e.err }

// safetyViolation is a repository-policy failure. Repair actors may fix a bad
// change, but they must never be asked to explain away a protected or
// out-of-scope edit.
type safetyViolation struct{ err error }

func (e *safetyViolation) Error() string { return e.err.Error() }
func (e *safetyViolation) Unwrap() error { return e.err }

// repairBudgetExhaustedError indicates that a validation repair budget has been exhausted.
type repairBudgetExhaustedError struct {
	validation string
	failure    error
}

func (e *repairBudgetExhaustedError) Error() string {
	return fmt.Sprintf("validation %s exhausted repair budget: %v", e.validation, e.failure)
}
func (e *repairBudgetExhaustedError) Unwrap() error { return e.failure }

// standaloneRepairState keeps the budget for validations that run outside a
// recoverable phase (for example flow and completion validations). Without a
// durable record, restarting the interpreter could turn repair-once into an
// unbounded repair loop.
type standaloneRepairState struct {
	Attempts int `json:"attempts"`
}

func (e *Engine) runPhase(ctx context.Context, id string) (runErr error) {
	p, err := e.phaseByID(id)
	if err != nil {
		return err
	}
	e.phase = p
	defer func() { e.phase = nil }()
	if p.If != "" {
		ok, err := e.bool(p, p.If)
		if err != nil {
			return fmt.Errorf("phase %s condition: %w", id, err)
		}
		if !ok {
			e.presenter().PhaseSkip(id, "condition is false")
			return nil
		}
	}

	if ok, sha, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil {
		return err
	} else if ok {
		if err := e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
			return fmt.Errorf("completed phase %s is no longer safe to skip: %w", id, err)
		}
		e.presenter().CompletedPhaseSkip(id, p.Label, sha)
		return nil
	}
	if p.Kind == "criterion" && (p.Criterion != "" || p.CriterionID != "") {
		_, targetText, targetErr := e.phaseCriterion(p)
		if targetErr != nil {
			return targetErr
		}
		checked, err := e.criterionChecked(targetText)
		if err != nil {
			return err
		}
		if checked {
			if p.AdvanceProgress {
				return fmt.Errorf("criterion phase %s target is already checked without accepted engine-owned progress state", id)
			}
			// A legacy already-checked shortcut still accepts workspace-derived
			// progress. Require the same clean mutation boundary as an ordinary
			// acceptance so an unrelated dirty edit cannot be claimed by the
			// deterministic gate and phase marker.
			if err := e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
				return fmt.Errorf("criterion phase %s cannot reuse checked progress safely: %w", id, err)
			}
			skip := e.Workflow.Spec.PhaseDefaults.Skip.CriterionAlreadyChecked
			if !skip.ValidateBeforeMarking {
				return fmt.Errorf("criterion phase %s is already checked but validation before marking is disabled", id)
			}
			validated := false
			actions := append([]workflow.PhaseAction{}, e.Workflow.Spec.PhaseDefaults.After...)
			actions = append(actions, p.After...)
			for _, action := range actions {
				if action.Validate == "" {
					continue
				}
				if action.If != "" {
					run, err := e.bool(p, action.If)
					if err != nil {
						return fmt.Errorf("phase action condition: %w", err)
					}
					if !run {
						continue
					}
				}
				if err := e.runValidation(ctx, action.Validate, p); err != nil {
					var safetyErr *safetyViolation
					if errors.As(err, &safetyErr) {
						return err
					}
					return &phaseValidationFailure{err: err}
				}
				validated = true
				break
			}
			if !validated {
				return fmt.Errorf("criterion phase %s is already checked but has no runnable deterministic validation", id)
			}
			if err := e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
				return fmt.Errorf("criterion phase %s failed its checked-progress safety boundary: %w", id, err)
			}
			head, _ := e.Repo.Head()
			if err := e.Store.SetCommit(e.phaseMarkerName(p), head); err != nil {
				return err
			}
			e.presenter().CriterionAlreadyChecked(id)
			return nil
		}
	}
	if err := e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
		return fmt.Errorf("phase %s cannot start safely: %w", id, err)
	}
	active, err := e.newActivePhase(p.ID)
	if err != nil {
		return err
	}
	if len(e.Workflow.Spec.PhaseDefaults.Before) > 0 {
		if err := e.runPhaseActions(ctx, p, &active, e.Workflow.Spec.PhaseDefaults.Before); err != nil {
			return err
		}
	}
	// A durable active record is a runtime safety invariant even for compact
	// workflows that omit the verbose lifecycle declarations.
	if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
		return err
	}

	e.presenter().PhaseStart(id, p.Label)
	e.logEvent("phase_start", map[string]string{"phase": id})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("phase_end", map[string]string{"phase": id, "result": result})
	}()
	if p.Kind == "bookkeeping" && len(p.Bookkeeping) > 0 {
		active.ActorCompleted = true // engine-only deterministic phase; no actor owns this transition.
		if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
			return fmt.Errorf("persist engine-owned bookkeeping phase %s: %w", p.ID, err)
		}
	} else if err := e.runPhaseActor(ctx, p, p.Prompt, &active); err != nil {
		return err
	} else {
		e.presentActorGitSummary(active.StartCommit)
	}
	return e.finishPhase(ctx, p, active)
}

// presentActorGitSummary is deliberately best-effort. It observes repository
// state after the provider returns, but its output never participates in
// validation, checkpointing, or advancement.
func (e *Engine) presentActorGitSummary(start string) {
	files, err := e.Repo.ChangedFilesSince(start)
	if err != nil {
		return
	}
	e.presenter().GitSummary("since phase start", e.filterIgnored(files))
}
func (e *Engine) finishPhase(ctx context.Context, p *workflow.Phase, active ActivePhase) error {
	if e.runtimeOwnsPhaseLifecycle(p) {
		actions, err := e.runtimePhaseActions(p)
		if err != nil {
			return err
		}
		active.ValidationPassed = false
		if err := e.runPhaseActions(ctx, p, &active, actions); err != nil {
			return err
		}
		if !active.ValidationPassed {
			return fmt.Errorf("phase %s did not run a successful deterministic validation before acceptance", p.ID)
		}
		return e.requirePhaseCompletion(p)
	}
	actions := append([]workflow.PhaseAction{}, e.Workflow.Spec.PhaseDefaults.After...)
	actions = append(actions, p.After...)
	if len(actions) == 0 {
		actions = []workflow.PhaseAction{
			{AssertProgressIfApplicable: true},
			{Checkpoint: ""},
			{AssertNetRepositoryChangeSincePhaseStart: p.RequiresChange},
			{MarkPhaseComplete: &workflow.Marker{Value: "head_commit"}},
			{ClearActivePhase: true},
		}
	}
	if !phaseHasValidation(actions) {
		return fmt.Errorf("phase %s has no deterministic validation before acceptance", p.ID)
	}
	// Require this acceptance attempt to run its own successful gate. Recovery
	// deliberately repeats the lifecycle rather than relying on a validation
	// result that may predate an interruption or external workspace change.
	active.ValidationPassed = false
	if err := e.runPhaseActions(ctx, p, &active, actions); err != nil {
		return err
	}
	if !active.ValidationPassed {
		return fmt.Errorf("phase %s did not run a successful deterministic validation before acceptance", p.ID)
	}
	return e.requirePhaseCompletion(p)
}

func phaseHasValidation(actions []workflow.PhaseAction) bool {
	for _, action := range actions {
		if action.Validate != "" {
			return true
		}
	}
	return false
}
func (e *Engine) recoverActive(ctx context.Context) error {
	var a ActivePhase
	ok, err := e.Store.GetJSON(e.activeRecord(), &a)
	if err != nil || !ok {
		return err
	}
	p, err := e.phaseByID(a.PhaseID)
	if err != nil {
		return err
	}
	if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
		return fmt.Errorf("cannot recover phase %s safely: %w", p.ID, err)
	}
	if !e.Repo.ObjectExists(a.StartCommit + "^{commit}") {
		return fmt.Errorf("saved phase start missing: %s", a.StartCommit)
	}
	if !e.Repo.IsAncestor(a.StartCommit, "HEAD") {
		return fmt.Errorf("HEAD no longer descends from interrupted phase start %s", a.StartCommit)
	}
	e.presenter().PhaseResume(p.ID, p.Label)
	if marked, _, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil {
		return err
	} else if marked {
		// A process may be interrupted between writing the commit-valued phase
		// marker and clearing active state. The marker is the acceptance record;
		// clear only the stale in-progress record and never rerun the actor.
		return e.Store.Delete(e.activeRecord())
	}
	if a.ActorCompleted {
		// The provider has durably returned. Resume only deterministic phase
		// acceptance; a successful gate is never evidence that an interrupted
		// actor completed, but it may complete a pending acceptance sequence.
		return e.finishPhase(ctx, p, a)
	}
	// A provider may have returned an error after leaving useful partial work or
	// a partial commit. Inspect that state with the deterministic phase gate
	// before asking the actor to continue. A passing gate still cannot authorize
	// acceptance because actor_completed is intentionally false; it only tells
	// the resumed actor that the retained state is internally coherent.
	if validation := e.phaseValidation(p); validation != "" {
		if err := e.validateExistingPhaseState(ctx, validation, p); err != nil {
			var safetyErr *safetyViolation
			if errors.As(err, &safetyErr) {
				if persistErr := e.persistSafetyFailure(p, err); persistErr != nil {
					return persistErr
				}
				return err
			}
			e.presenter().RetainedWorkResume(p.Actor)
		} else {
			e.presenter().RetainedWorkPreflight()
		}
	}
	prompt := "Resume this phase from the repository state already present.\nInspect partial commits and working-tree changes first; preserve correct work and finish only this phase's objective.\n\n" + p.Prompt
	if err := e.runPhaseActor(ctx, p, prompt, &a); err != nil {
		return err
	}
	return e.finishPhase(ctx, p, a)
}

func (e *Engine) validateExistingPhaseState(ctx context.Context, name string, p *workflow.Phase) error {
	v, ok := e.Workflow.Spec.Validation[name]
	if !ok {
		return fmt.Errorf("unknown validation %q", name)
	}
	return e.runToolUses(ctx, v.Steps, p)
}

// runPhaseActor records the only evidence that authorizes deterministic phase
// acceptance: the primary phase actor returned successfully. It intentionally
// does not cover repair actors; their bounded invocation is governed by their
// validation policy, while this record answers whether the phase itself ran.
func (e *Engine) runPhaseActor(ctx context.Context, p *workflow.Phase, prompt string, active *ActivePhase) error {
	if err := e.runAgent(ctx, p.Actor, p.Reasoning, prompt, p); err != nil {
		if policyErr := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); policyErr != nil {
			var safetyErr *safetyViolation
			if errors.As(policyErr, &safetyErr) {
				if persistErr := e.persistSafetyFailure(p, policyErr); persistErr != nil {
					return persistErr
				}
			}
			return policyErr
		}
		return err
	}
	active.ActorCompleted = true
	if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
		return fmt.Errorf("persist successful actor completion for phase %s: %w", p.ID, err)
	}
	return nil
}
func (e *Engine) runAgent(ctx context.Context, actorName, reasoning, prompt string, p *workflow.Phase) error {
	a, ok := e.Workflow.Spec.Agents[actorName]
	if !ok {
		return fmt.Errorf("unknown actor %q", actorName)
	}
	prov, ok := e.Providers[a.Runner]
	if !ok {
		return fmt.Errorf("no provider registered for runner %q", a.Runner)
	}
	x := e.context(p)
	model, err := x.Expand(a.Model)
	if err != nil {
		return err
	}
	prompt, err = x.Expand(prompt)
	if err != nil {
		return err
	}
	metadata := map[string]string{"actor": actorName}
	if p != nil {
		metadata["phase"] = p.ID
		metadata["phase_kind"] = p.Kind
		metadata["criterion"] = p.Criterion
		if p.Kind == "criterion" {
			if criterionID, criterionText, criterionErr := e.phaseCriterion(p); criterionErr == nil {
				metadata["criterion"] = criterionText
				metadata["criterion_id"] = criterionID
			}
		}
	}
	e.presenter().ProviderIdentity(prov.Name(), actorName)
	e.logEvent("provider_start", map[string]string{"provider": prov.Name(), "actor": actorName})
	presentation := provider.ResolvePresentationIntent(a.Color)
	if e.detached {
		// Detached stdout and stderr are durable diagnostic streams. Keep this
		// boundary explicit so a provider cannot infer terminal presentation from
		// an adapter-specific or test-supplied TTY signal.
		presentation = provider.PresentationPlain
	}
	_, err = prov.Run(ctx, provider.Request{
		Workspace:    e.Repo.Root,
		Model:        model,
		Reasoning:    reasoning,
		Prompt:       prompt,
		Sandbox:      a.Sandbox,
		Approval:     a.Approval,
		Ephemeral:    a.Ephemeral,
		Presentation: presentation,
		Metadata:     metadata,
	})
	if err != nil {
		e.logEvent("provider_end", map[string]string{"provider": prov.Name(), "actor": actorName, "result": "failure"})
		return fmt.Errorf("provider %s actor %s: %w", prov.Name(), actorName, err)
	}
	e.logEvent("provider_end", map[string]string{"provider": prov.Name(), "actor": actorName, "result": "success"})
	return nil
}
func (e *Engine) runValidation(ctx context.Context, name string, p *workflow.Phase) (runErr error) {
	e.logEvent("validation_start", map[string]string{"validation": name})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("validation_end", map[string]string{"validation": name, "result": result})
	}()
	v, ok := e.Workflow.Spec.Validation[name]
	if !ok {
		for _, step := range e.Workflow.Spec.Flow {
			if step.ID == name && step.Validate != "" {
				name = step.Validate
				v, ok = e.Workflow.Spec.Validation[name]
				break
			}
		}
	}
	if !ok {
		return fmt.Errorf("unknown validation %q", name)
	}
	key, cacheable, keyErr := e.validationEvidenceKey(name, v, p)
	if keyErr != nil {
		return keyErr
	}
	if cacheable {
		// Evidence is never a substitute for the safety boundary. In
		// particular, a successful gate from a clean lineage cannot authorize
		// acceptance after a protected-file or scope violation.
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
			if p != nil {
				_ = e.persistSafetyFailure(p, err)
			} else {
				_ = e.persistValidationFailure(nil, name, err)
			}
			return err
		}
		if hit, err := e.loadValidationEvidence(key); err != nil {
			return err
		} else if hit {
			if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
				if p != nil {
					_ = e.persistSafetyFailure(p, err)
				} else {
					_ = e.persistValidationFailure(nil, name, err)
				}
				return err
			}
			e.presenter().ValidationReuse(name)
			if clearErr := e.clearValidationFailure(p, name); clearErr != nil {
				return clearErr
			}
			return nil
		}
	}
	err := e.runToolUses(ctx, v.Steps, p)
	if err == nil {
		if cacheable {
			// A deterministic validation must not publish success for a different
			// tree than the one it inspected. This also prevents a tool that
			// accidentally mutates an allowed file from creating reusable proof.
			finalKey, _, finalErr := e.validationEvidenceKey(name, v, p)
			if finalErr != nil {
				return finalErr
			}
			if finalKey != key {
				return fmt.Errorf("validation %s changed relevant inputs while running; success evidence was not recorded", name)
			}
			if boundaryErr := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); boundaryErr != nil {
				if p != nil {
					_ = e.persistSafetyFailure(p, boundaryErr)
				} else {
					_ = e.persistValidationFailure(nil, name, boundaryErr)
				}
				return boundaryErr
			}
			if err := e.persistValidationEvidence(key); err != nil {
				return fmt.Errorf("persist validation %s evidence: %w", name, err)
			}
		}
		if clearErr := e.clearValidationFailure(p, name); clearErr != nil {
			return clearErr
		}
		return nil
	}
	failure := err
	if persistErr := e.persistValidationFailure(p, name, failure); persistErr != nil {
		return persistErr
	}
	var safetyErr *safetyViolation
	if errors.As(failure, &safetyErr) {
		return failure
	}
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts < 1 {
		return fmt.Errorf("validation %s failed: %w", name, failure)
	}
	available, budgetErr := e.consumeRepairAttempt(name, p, v.OnFailure.MaxRepairAttempts)
	if budgetErr != nil {
		return budgetErr
	}
	if !available {
		return &repairBudgetExhaustedError{validation: name, failure: failure}
	}
	e.lastFailure = boundedFailureOutput(failure)
	e.presenter().RepairAttempt(name)
	if err := e.runAgent(ctx, v.OnFailure.Repair.Actor, v.OnFailure.Repair.Reasoning, v.OnFailure.Repair.Prompt, p); err != nil {
		return err
	}
	steps := v.OnFailure.Then
	if len(steps) == 0 {
		// A repair policy always re-runs deterministic validation. The explicit
		// `then` list can narrow or extend that rerun, but omitting it must not
		// make a failed validation succeed merely because no tools were invoked.
		steps = v.Steps
	}
	if err := e.runToolUses(ctx, steps, p); err != nil {
		return fmt.Errorf("validation %s still fails after repair: %w", name, err)
	}
	if cacheable {
		finalKey, _, keyErr := e.validationEvidenceKey(name, v, p)
		if keyErr != nil {
			return keyErr
		}
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
			if p != nil {
				_ = e.persistSafetyFailure(p, err)
			} else {
				_ = e.persistValidationFailure(nil, name, err)
			}
			return err
		}
		if err := e.persistValidationEvidence(finalKey); err != nil {
			return fmt.Errorf("persist validation %s evidence after repair: %w", name, err)
		}
	}
	e.lastFailure = ""
	if clearErr := e.clearValidationFailure(p, name); clearErr != nil {
		return clearErr
	}
	return nil
}

func (e *Engine) persistValidationFailure(p *workflow.Phase, name string, failure error) error {
	if p == nil {
		record := e.standaloneFailureRecord(name)
		kind := PhaseFailureValidation
		var safetyErr *safetyViolation
		if errors.As(failure, &safetyErr) {
			kind = PhaseFailureSafety
		}
		return e.Store.SetJSON(record, validationFailureEvidence{
			Validation: name, FailureKind: kind, Output: errorOutput(failure),
		})
	}
	var active ActivePhase
	ok, err := e.Store.GetJSON(e.activeRecord(), &active)
	if err != nil || !ok || active.PhaseID != p.ID {
		return err
	}
	active.FailureKind = PhaseFailureValidation
	var safetyErr *safetyViolation
	if errors.As(failure, &safetyErr) {
		active.FailureKind = PhaseFailureSafety
	}
	active.Validation = name
	active.ValidationError = errorOutput(failure)
	return e.Store.SetJSON(e.activeRecord(), active)
}

func (e *Engine) persistSafetyFailure(p *workflow.Phase, failure error) error {
	if p == nil {
		return nil
	}
	var active ActivePhase
	ok, err := e.Store.GetJSON(e.activeRecord(), &active)
	if err != nil {
		return err
	}
	if !ok || active.PhaseID != p.ID {
		return nil
	}
	active.FailureKind = PhaseFailureSafety
	if active.Validation == "" {
		active.Validation = "workspace-policy"
	}
	active.ValidationError = errorOutput(failure)
	return e.Store.SetJSON(e.activeRecord(), active)
}

func (e *Engine) clearValidationFailure(p *workflow.Phase, name string) error {
	if p != nil {
		var active ActivePhase
		ok, err := e.Store.GetJSON(e.activeRecord(), &active)
		if err != nil {
			return err
		}
		if ok && active.PhaseID == p.ID {
			if active.Validation != name {
				return nil
			}
			active.FailureKind = ""
			active.Validation = ""
			active.ValidationError = ""
			return e.Store.SetJSON(e.activeRecord(), active)
		}
	}
	if _, ok, err := e.Store.Resolve(e.standaloneFailureRecord(name)); err != nil || !ok {
		return err
	}
	if err := e.Store.Delete(e.standaloneFailureRecord(name)); err != nil {
		return err
	}
	return e.clearStandaloneRepairState(name)
}

type validationFailureEvidence struct {
	Validation  string           `json:"validation"`
	FailureKind PhaseFailureKind `json:"failure_kind"`
	Output      string           `json:"output,omitempty"`
}

func (e *Engine) standaloneFailureRecord(validation string) string {
	return fmt.Sprintf("validation-failures/%x", validation)
}

// consumeRepairAttempt persists the applicable repair budget before invoking a
// repair actor. A crash during repair therefore cannot reset the budget and
// turn a one-shot repair policy into an unbounded restart loop.
func (e *Engine) consumeRepairAttempt(validation string, p *workflow.Phase, max int) (bool, error) {
	if p != nil {
		var active ActivePhase
		ok, err := e.Store.GetJSON(e.activeRecord(), &active)
		if err != nil {
			return false, err
		}
		if ok && active.PhaseID == p.ID {
			if active.RepairAttempts[validation] >= max {
				return false, nil
			}
			if active.RepairAttempts == nil {
				active.RepairAttempts = map[string]int{}
			}
			active.RepairAttempts[validation]++
			if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return e.consumeStandaloneRepairAttempt(validation, max)
}

func (e *Engine) standaloneRepairRecord(validation string) string {
	return fmt.Sprintf("validation-repairs/%x", validation)
}

func (e *Engine) consumeStandaloneRepairAttempt(validation string, max int) (bool, error) {
	record := e.standaloneRepairRecord(validation)
	var state standaloneRepairState
	ok, err := e.Store.GetJSON(record, &state)
	if err != nil {
		return false, err
	}
	if ok && state.Attempts >= max {
		return false, nil
	}
	state.Attempts++
	if err := e.Store.SetJSON(record, state); err != nil {
		return false, err
	}
	return true, nil
}

func (e *Engine) clearStandaloneRepairState(validation string) error {
	record := e.standaloneRepairRecord(validation)
	_, ok, err := e.Store.Resolve(record)
	if err != nil || !ok {
		return err
	}
	return e.Store.Delete(record)
}
func (e *Engine) runToolUses(ctx context.Context, steps []workflow.ToolUse, p *workflow.Phase) error {
	for _, use := range steps {
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
			return err
		}
		if use.If != "" {
			ok, err := e.bool(p, use.If)
			if err != nil {
				return fmt.Errorf("tool %s condition: %w", use.Uses, err)
			}
			if !ok {
				continue
			}
		}
		if err := e.runToolUse(ctx, use, p); err != nil {
			if policyErr := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); policyErr != nil {
				return policyErr
			}
			return err
		}
		if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) runTool(ctx context.Context, name string, p *workflow.Phase) error {
	return e.runToolUse(ctx, workflow.ToolUse{Uses: name}, p)
}
func (e *Engine) runToolUse(ctx context.Context, use workflow.ToolUse, p *workflow.Phase) (runErr error) {
	name := use.Uses
	e.logEvent("tool_start", map[string]string{"tool": name})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("tool_end", map[string]string{"tool": name, "result": result})
	}()
	t, ok := e.Workflow.Spec.Tools[name]
	if !ok {
		return fmt.Errorf("unknown tool %q", name)
	}
	switch t.Type {
	case "workspace-policy":
		return e.assertScope()
	case "shell":
		cmdline, err := e.context(p).Expand(t.Command)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
		cmd.Dir = e.Repo.Root
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err = cmd.Run()
		output := stdout.String() + stderr.String()
		clioutput.NewPresenterWithPresentation(e.Out, clioutput.PresentationRaw).Raw(output)
		if t.Capture.Log != "" {
			logPath, expandErr := e.context(p).Expand(t.Capture.Log)
			if expandErr != nil {
				return expandErr
			}
			target := logPath
			if !filepath.IsAbs(target) {
				target = filepath.Join(e.Repo.Root, target)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(output), 0o644); err != nil {
				return err
			}
		}
		if err != nil {
			e.lastFailure = output
			return fmt.Errorf("shell tool %s failed: %w\n%s", name, err, output)
		}
		return nil
	case "git-checkpoint":
		return e.checkpoint(name, p)
	case "file-regex":
		path, err := e.context(p).Expand(use.With.Path)
		if err != nil {
			return err
		}
		regex, err := e.context(p).Expand(use.With.Regex)
		if err != nil {
			return err
		}
		return e.assertFileRegex(path, regex)
	case "markdown-checklist-progress":
		if e.Workflow.Spec.Progress.Source.Path == "" {
			return fmt.Errorf("markdown-checklist-progress requires spec.progress.source.path")
		}
		if _, err := e.progressSnapshot(); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unsupported tool type %q for %s", t.Type, name)
	}
}
