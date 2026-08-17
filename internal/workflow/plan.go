package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// ExpandedPlan is a read-only explanation of the actual executable contract.
// It intentionally contains no provider invocation or mutable tool operation.
type ExpandedPlan struct {
	Workflow                string              `yaml:"workflow"`
	ResolvedLifecycle       LifecyclePolicy     `yaml:"resolvedLifecycle"`
	SafetyEnforcementPoints []string            `yaml:"safetyEnforcementPoints"`
	RecoveryBehavior        []string            `yaml:"recoveryBehavior"`
	Validations             []PlannedValidation `yaml:"validations"`
	Phases                  []PlannedPhase      `yaml:"phases"`
	ProgressTransitions     []string            `yaml:"progressTransitions"`
	CheckpointBehavior      string              `yaml:"checkpointBehavior"`
	HumanGates              []string            `yaml:"humanGates"`
	CompletionContract      []string            `yaml:"completionContract"`
}
type PlannedValidation struct {
	Name   string   `yaml:"name"`
	Steps  []string `yaml:"steps"`
	Repair string   `yaml:"repair"`
}
type PlannedPhase struct {
	ID         string   `yaml:"id"`
	Kind       string   `yaml:"kind"`
	Actor      string   `yaml:"actor,omitempty"`
	Validation string   `yaml:"validation"`
	Acceptance []string `yaml:"acceptance"`
}

func BuildExpandedPlan(d *Document) (ExpandedPlan, error) {
	n, err := NormalizeWorkflow(d)
	if err != nil {
		return ExpandedPlan{}, err
	}
	if r := validateOnly(n); r.Status == Invalid {
		return ExpandedPlan{}, fmt.Errorf("normalized workflow is invalid")
	}
	w := n.Workflow
	plan := ExpandedPlan{
		Workflow:                w.Metadata.Name,
		ResolvedLifecycle:       w.Spec.Lifecycle,
		SafetyEnforcementPoints: []string{"before actor and tool work", "after actor and tool work", "before checkpoint", "before acceptance marker reuse", "during interrupted-phase recovery"},
		RecoveryBehavior:        []string{"completed commit marker wins over stale active state", "actor_completed resumes deterministic acceptance without replaying the actor", "otherwise validate retained work before rerunning the same phase actor", "safety failures are terminal and never repaired by an actor"},
		CheckpointBehavior:      "runtime checkpoints accepted allowed dirty work, rechecks lineage, integrity, scope, and cleanliness, then writes the commit-valued phase marker",
	}
	for _, name := range sortedKeys(w.Spec.Validation) {
		v := w.Spec.Validation[name]
		steps := make([]string, 0, len(v.Steps))
		for _, step := range v.Steps {
			steps = append(steps, toolUseText(step))
		}
		repair := "fail-workflow"
		if v.OnFailure.Strategy == "repair-once" {
			repair = "one repair actor attempt; rerun the same deterministic validation steps"
			if len(v.OnFailure.Then) > 0 {
				repair = "one repair actor attempt; run declared post-repair deterministic steps"
			}
		}
		plan.Validations = append(plan.Validations, PlannedValidation{Name: name, Steps: steps, Repair: repair})
	}
	for _, p := range w.Spec.Phases {
		validation := p.Validation
		if validation == "" {
			validation = w.Spec.Lifecycle.Validation
		}
		acceptance := []string{"persist active phase"}
		if p.Kind == "bookkeeping" && len(p.Bookkeeping) > 0 {
			acceptance = append(acceptance, "deterministic bookkeeping")
		} else {
			acceptance = append(acceptance, "run actor", "persist actor_completed")
		}
		acceptance = append(acceptance, "deterministic validation")
		if p.Kind == "criterion" {
			acceptance = append(acceptance, "assert declared progress transition")
		}
		if p.RequiresChange {
			acceptance = append(acceptance, "assert net repository change")
		}
		acceptance = append(acceptance, "checkpoint", "write completed commit marker", "clear active phase")
		plan.Phases = append(plan.Phases, PlannedPhase{ID: p.ID, Kind: p.Kind, Actor: p.Actor, Validation: validation, Acceptance: acceptance})
		if p.Kind == "criterion" && p.AdvanceProgress {
			plan.ProgressTransitions = append(plan.ProgressTransitions, p.ID+": engine advances only criterionID after validation")
		}
	}
	for _, gate := range w.Spec.HumanGates {
		plan.HumanGates = append(plan.HumanGates, gate.ID+": acknowledgement and durable evidence required")
	}
	for _, name := range sortedKeys(w.Spec.Completion) {
		c := w.Spec.Completion[name]
		parts := []string{name}
		if c.FinalValidation != "" {
			parts = append(parts, "final validation "+c.FinalValidation)
		}
		parts = append(parts, "checkpoint", "after-checkpoint assertions", "write completion marker")
		plan.CompletionContract = append(plan.CompletionContract, strings.Join(parts, ": "))
	}
	sort.Strings(plan.HumanGates)
	return plan, nil
}

func toolUseText(u ToolUse) string {
	if u.If == "" {
		return u.Uses
	}
	return u.Uses + " if " + u.If
}
