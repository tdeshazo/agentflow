package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

type repairBudgetExhaustedError struct {
	validation string
	failure    error
}

func (e *repairBudgetExhaustedError) Error() string {
	return fmt.Sprintf("validation %s exhausted repair budget: %v", e.validation, e.failure)
}
func (e *repairBudgetExhaustedError) Unwrap() error { return e.failure }

func (e *Engine) runPhase(ctx context.Context, id string) error {
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
			fmt.Fprintf(e.Out, "==> Skipping phase %s: condition is false\n", id)
			return nil
		}
	}

	if ok, sha, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil {
		return err
	} else if ok {
		fmt.Fprintf(e.Out, "==> Skipping completed phase %s: %s (%s)\n", id, p.Label, sha)
		return nil
	}
	if p.Kind == "criterion" && p.Criterion != "" {
		checked, err := e.criterionChecked(p.Criterion)
		if err != nil {
			return err
		}
		if checked {
			skip := e.Workflow.Spec.PhaseDefaults.Skip.CriterionAlreadyChecked
			if skip.ValidateBeforeMarking {
				for _, action := range e.Workflow.Spec.PhaseDefaults.After {
					if action.Validate == "" {
						continue
					}
					if err := e.runValidation(ctx, action.Validate, p); err != nil {
						return &phaseValidationFailure{err: err}
					}
					break
				}
			}
			head, _ := e.Repo.Head()
			if err := e.Store.SetCommit(e.phaseMarkerName(p), head); err != nil {
				return err
			}
			fmt.Fprintf(e.Out, "==> Criterion already checked; marking phase %s complete\n", id)
			return nil
		}
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("phase %s cannot start with unexplained dirty files: %s", id, strings.Join(dirty, ", "))
	}
	active, err := e.newActivePhase(id)
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

	fmt.Fprintf(e.Out, "==> Phase %s: %s\n", id, p.Label)
	if err := e.runAgent(ctx, p.Actor, p.Reasoning, p.Prompt, p); err != nil {
		return err
	}
	return e.finishPhase(ctx, p, active)
}
func (e *Engine) finishPhase(ctx context.Context, p *workflow.Phase, active ActivePhase) error {
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
	if err := e.runPhaseActions(ctx, p, &active, actions); err != nil {
		return err
	}
	return e.requirePhaseCompletion(p)
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
	if !e.Repo.ObjectExists(a.StartCommit + "^{commit}") {
		return fmt.Errorf("saved phase start missing: %s", a.StartCommit)
	}
	if !e.Repo.IsAncestor(a.StartCommit, "HEAD") {
		return fmt.Errorf("HEAD no longer descends from interrupted phase start %s", a.StartCommit)
	}
	fmt.Fprintf(e.Out, "==> Recovering interrupted phase %s: %s\n", p.ID, p.Label)
	if marked, _, err := e.validCommitMarker(e.phaseMarkerName(p)); err != nil {
		return err
	} else if marked {
		// A process may be interrupted between writing the commit-valued phase
		// marker and clearing active state. The marker is the acceptance record;
		// clear only the stale in-progress record and never rerun the actor.
		return e.Store.Delete(e.activeRecord())
	}
	if p.Kind == "criterion" && p.Criterion != "" {
		checked, err := e.criterionChecked(p.Criterion)
		if err != nil {
			return err
		}
		if checked {
			return e.finishPhase(ctx, p, a)
		}
	} else {
		// An implementation/audit phase has no external checkbox to tell us
		// whether its actor finished. Re-run the normal deterministic acceptance
		// path first. This accepts a useful committed or dirty partial result only
		// when every normal gate passes; it also covers interruption after a
		// checkpoint but before the phase marker was written.
		if err := e.finishPhase(ctx, p, a); err == nil {
			return nil
		} else {
			var exhausted *repairBudgetExhaustedError
			if errors.As(err, &exhausted) {
				return err
			}
			var validationErr *phaseValidationFailure
			if !errors.As(err, &validationErr) {
				return err
			}
		}
	}
	prompt := "Resume this phase from the repository state already present.\nInspect partial commits and working-tree changes first; preserve correct work and finish only this phase's objective.\n\n" + p.Prompt
	if err := e.runAgent(ctx, p.Actor, p.Reasoning, prompt, p); err != nil {
		return err
	}
	return e.finishPhase(ctx, p, a)
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
	}
	_, err = prov.Run(ctx, provider.Request{Workspace: e.Repo.Root, Model: model, Reasoning: reasoning, Prompt: prompt, Sandbox: a.Sandbox, Approval: a.Approval, Ephemeral: a.Ephemeral, Color: a.Color, Metadata: metadata})
	if err != nil {
		return fmt.Errorf("provider %s actor %s: %w", prov.Name(), actorName, err)
	}
	return nil
}
func (e *Engine) runValidation(ctx context.Context, name string, p *workflow.Phase) error {
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
	err := e.runToolUses(ctx, v.Steps, p)
	if err == nil {
		return nil
	}
	failure := err
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
	e.lastFailure = errorOutput(failure)
	fmt.Fprintf(e.Out, "==> Validation %s failed; running one repair attempt\n", name)
	if err := e.runAgent(ctx, v.OnFailure.Repair.Actor, v.OnFailure.Repair.Reasoning, v.OnFailure.Repair.Prompt, p); err != nil {
		return err
	}
	if err := e.runToolUses(ctx, v.OnFailure.Then, p); err != nil {
		return fmt.Errorf("validation %s still fails after repair: %w", name, err)
	}
	e.lastFailure = ""
	return nil
}

// consumeRepairAttempt persists a phase-local budget before invoking a repair
// actor. A crash during repair therefore cannot reset the budget and turn a
// one-shot repair policy into an unbounded restart loop.
func (e *Engine) consumeRepairAttempt(validation string, p *workflow.Phase, max int) (bool, error) {
	if p == nil {
		return true, nil
	}
	var active ActivePhase
	ok, err := e.Store.GetJSON(e.activeRecord(), &active)
	if err != nil {
		return false, err
	}
	if !ok || active.PhaseID != p.ID {
		return true, nil
	}
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
func (e *Engine) runToolUses(ctx context.Context, steps []workflow.ToolUse, p *workflow.Phase) error {
	for _, use := range steps {
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
			return err
		}
	}
	return nil
}
func (e *Engine) runTool(ctx context.Context, name string, p *workflow.Phase) error {
	return e.runToolUse(ctx, workflow.ToolUse{Uses: name}, p)
}
func (e *Engine) runToolUse(ctx context.Context, use workflow.ToolUse, p *workflow.Phase) error {
	name := use.Uses
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
		fmt.Fprint(e.Out, output)
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
