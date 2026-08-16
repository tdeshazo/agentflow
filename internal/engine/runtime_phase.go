package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

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

	if ok, sha, err := e.validCommitMarker("phases/" + id); err != nil {
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
			head, _ := e.Repo.Head()
			if err := e.Store.SetCommit("phases/"+id, head); err != nil {
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
	if err := e.Store.SetJSON("active", active); err != nil {
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
		return e.finishPhaseLegacy(ctx, p, active)
	}
	if err := e.runPhaseActions(ctx, p, &active, actions); err != nil {
		return err
	}
	return e.requirePhaseCompletion(p)
}

func (e *Engine) finishPhaseLegacy(ctx context.Context, p *workflow.Phase, active ActivePhase) error {
	if err := e.assertScope(); err != nil {
		return err
	}
	if _, ok := e.Workflow.Spec.Validation["phaseGate"]; ok {
		if err := e.runValidation(ctx, "phaseGate", p); err != nil {
			return err
		}
	}
	if p.Kind == "criterion" {
		if err := e.assertProgress(p, active); err != nil {
			return err
		}
	}
	if err := e.checkpoint(p.Label, p); err != nil {
		return err
	}
	if p.RequiresChange {
		if err := e.assertNetChange(p, active); err != nil {
			return err
		}
	}
	if err := e.markPhaseComplete(p); err != nil {
		return err
	}
	if err := e.Store.Delete("active"); err != nil {
		return err
	}
	return e.requirePhaseCompletion(p)
}
func (e *Engine) recoverActive(ctx context.Context) error {
	var a ActivePhase
	ok, err := e.Store.GetJSON("active", &a)
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
	if p.Kind == "criterion" && p.Criterion != "" {
		checked, err := e.criterionChecked(p.Criterion)
		if err != nil {
			return err
		}
		if checked {
			return e.finishPhase(ctx, p, a)
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
	_, err = prov.Run(ctx, provider.Request{Workspace: e.Repo.Root, Model: model, Reasoning: reasoning, Prompt: prompt, Sandbox: a.Sandbox, Approval: a.Approval, Ephemeral: a.Ephemeral, Color: a.Color, Metadata: map[string]string{"actor": actorName}})
	if err != nil {
		return fmt.Errorf("provider %s actor %s: %w", prov.Name(), actorName, err)
	}
	return nil
}
func (e *Engine) runValidation(ctx context.Context, name string, p *workflow.Phase) error {
	v, ok := e.Workflow.Spec.Validation[name]
	if !ok {
		return fmt.Errorf("unknown validation %q", name)
	}
	err := e.runToolUses(v.Steps, p)
	if err == nil {
		return nil
	}
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts < 1 {
		return fmt.Errorf("validation %s failed: %w", name, err)
	}
	e.lastFailure = errorOutput(err)
	fmt.Fprintf(e.Out, "==> Validation %s failed; running one repair attempt\n", name)
	if err := e.runAgent(ctx, v.OnFailure.Repair.Actor, v.OnFailure.Repair.Reasoning, v.OnFailure.Repair.Prompt, p); err != nil {
		return err
	}
	if err := e.runToolUses(v.OnFailure.Then, p); err != nil {
		return fmt.Errorf("validation %s still fails after repair: %w", name, err)
	}
	e.lastFailure = ""
	return nil
}
func (e *Engine) runToolUses(steps []workflow.ToolUse, p *workflow.Phase) error {
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
		if err := e.runToolUse(use, p); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) runTool(name string, p *workflow.Phase) error {
	return e.runToolUse(workflow.ToolUse{Uses: name}, p)
}
func (e *Engine) runToolUse(use workflow.ToolUse, p *workflow.Phase) error {
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
		cmd := exec.Command("sh", "-c", cmdline)
		cmd.Dir = e.Repo.Root
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err = cmd.Run()
		fmt.Fprint(e.Out, buf.String())
		if err != nil {
			e.lastFailure = buf.String()
			return fmt.Errorf("shell tool %s failed: %w\n%s", name, err, buf.String())
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
	default:
		return fmt.Errorf("unsupported tool type %q for %s", t.Type, name)
	}
}
