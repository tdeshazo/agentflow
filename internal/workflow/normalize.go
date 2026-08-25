package workflow

import "fmt"

// NormalizeWorkflow compiles the selected authoring layer into the shared
// executable concepts. v1alpha2 remains marked as v1alpha2 so callers cannot
// mistake its unimplemented dependency execution for v1alpha1 behavior.
func NormalizeWorkflow(d *Document) (*Document, error) {
	if d != nil && d.V1Alpha2 != nil {
		return normalizeV1Alpha2(d.V1Alpha2, d.Locations)
	}
	if d == nil || d.Workflow == nil {
		return nil, fmt.Errorf("empty workflow document")
	}
	w := *d.Workflow
	w.Spec = d.Workflow.Spec
	w.Spec.Agents = make(map[string]Agent, len(d.Workflow.Spec.Agents))
	for id, local := range d.Workflow.Spec.Agents {
		w.Spec.Agents[id] = mergeAgent(d.Workflow.Spec.Defaults.Agent, local)
	}
	if w.Spec.Lifecycle.Policy == "" && w.Spec.Lifecycle.Validation == "" && w.Spec.Lifecycle.Checkpoint == "" {
		w.Spec.Lifecycle = d.Workflow.Spec.Defaults.Lifecycle
	}
	w.Spec.Phases = append([]Phase(nil), d.Workflow.Spec.Phases...)
	for i := range w.Spec.Phases {
		p := &w.Spec.Phases[i]
		if t, ok := d.Workflow.Spec.Defaults.Phases[p.Kind]; ok {
			if p.Actor == "" && !(p.Kind == "bookkeeping" && len(p.Bookkeeping) > 0) {
				p.Actor = t.Actor
			}
			if p.Reasoning == "" {
				p.Reasoning = t.Reasoning
			}
			if p.Validation == "" {
				p.Validation = t.Validation
			}
			if t.RequiresChange != nil && !p.present["requiresChange"] {
				p.RequiresChange = *t.RequiresChange
			}
			if p.Validation == "" {
				p.Validation = w.Spec.Lifecycle.Validation
			}
		}
	}
	w.Spec.Flow = append([]FlowStep(nil), d.Workflow.Spec.Flow...)
	for i := range w.Spec.Flow {
		loop := w.Spec.Flow[i].Loop
		if loop == nil || len(loop.DispatchByCriterion) == 0 {
			continue
		}
		byID := make(map[string]string, len(loop.DispatchByCriterion))
		for _, key := range sortedKeys(loop.DispatchByCriterion) {
			phase := loop.DispatchByCriterion[key]
			id, err := criterionIDForDispatchKey(d.Workflow, key)
			if err != nil {
				return nil, fmt.Errorf("flow[%d] dispatch criterion %q: %w", i, key, err)
			}
			if previous, exists := byID[id]; exists && previous != phase {
				return nil, fmt.Errorf("flow[%d] dispatches criterion %q to both phase %q and %q", i, id, previous, phase)
			}
			byID[id] = phase
		}
		loop.DispatchByCriterion = byID
	}
	w.Spec.Validation = make(map[string]Validation, len(d.Workflow.Spec.Validation))
	for id, v := range d.Workflow.Spec.Validation {
		if v.Repair == "once" {
			v.Repair = ""
			v.OnFailure.Strategy = "repair-once"
			v.OnFailure.MaxRepairAttempts = 1
		}
		if v.OnFailure.Strategy == "repair-once" {
			if v.OnFailure.MaxRepairAttempts == 0 {
				v.OnFailure.MaxRepairAttempts = 1
			}
			v.OnFailure.Repair = mergeRepair(d.Workflow.Spec.Defaults.Repair, v.OnFailure.Repair)
		}
		w.Spec.Validation[id] = v
	}
	return &Document{Workflow: &w, Locations: d.Locations}, nil
}

// criterionIDForDispatchKey compiles the legacy display-text loop selector to
// the immutable ID used by the runtime. IDs take precedence so a v1alpha1
// document remains deterministic even when an ID happens to equal another
// criterion's display text.
func criterionIDForDispatchKey(w *Workflow, key string) (string, error) {
	for _, criterion := range w.Spec.Progress.Criteria {
		if criterion.ID == key {
			return criterion.ID, nil
		}
	}
	var matches []Criterion
	for _, criterion := range w.Spec.Progress.Criteria {
		if criterion.Text == key {
			matches = append(matches, criterion)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("must name exactly one declared criterion ID or text")
	}
	return matches[0].ID, nil
}

func mergeAgent(base, local Agent) Agent {
	out := base
	if local.present == nil {
		return local
	} // programmatic workflows are already executable values.
	if local.present["runner"] {
		out.Runner = local.Runner
	}
	if local.present["model"] {
		out.Model = local.Model
	}
	if local.present["sandbox"] {
		out.Sandbox = local.Sandbox
	}
	if local.present["approval"] {
		out.Approval = local.Approval
	}
	if local.present["ephemeral"] {
		out.Ephemeral = local.Ephemeral
	}
	if local.present["may_commit"] {
		out.MayCommit = local.MayCommit
	}
	if local.present["output_last_message"] {
		out.OutputLastMessage = local.OutputLastMessage
	}
	out.present = nil
	return out
}

func mergeRepair(base, local Repair) Repair {
	if local.Actor == "" {
		local.Actor = base.Actor
	}
	if local.Reasoning == "" {
		local.Reasoning = base.Reasoning
	}
	if local.Prompt == "" {
		local.Prompt = base.Prompt
	}
	return local
}
