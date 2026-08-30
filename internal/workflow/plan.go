package workflow

import (
	"fmt"
	"sort"
	"strings"
)

// ExpandedPlan is a read-only explanation of the actual executable contract.
// It intentionally contains no provider invocation or mutable tool operation.
type ExpandedPlan struct {
	Workflow string `yaml:"workflow"`
	// NormalizedExecution is the complete executable projection after concise
	// defaults and version-specific syntax have been lowered. It deliberately
	// retains every shared workflow field that can affect execution so plan
	// output is a comparison surface, not a summary that hides policy.
	// Authoring defaults are cleared because their resolved values appear at
	// their authority-owning locations below.
	NormalizedExecution        Workflow             `yaml:"normalizedExecution"`
	WorkspaceMutationAllowlist []string             `yaml:"workspaceMutationAllowlist"`
	ResolvedAgents             []PlannedAgent       `yaml:"resolvedAgents"`
	ResolvedLifecycle          LifecyclePolicy      `yaml:"resolvedLifecycle"`
	SafetyEnforcementPoints    []string             `yaml:"safetyEnforcementPoints"`
	RecoveryBehavior           []string             `yaml:"recoveryBehavior"`
	Validations                []PlannedValidation  `yaml:"validations"`
	TypedContracts             PlannedContracts     `yaml:"typedContracts,omitempty"`
	Criteria                   PlannedCriteria      `yaml:"criteria,omitempty"`
	DependencyGraph            PhaseDependencyGraph `yaml:"dependencyGraph"`
	Phases                     []PlannedPhase       `yaml:"phases"`
	ProgressTransitions        []string             `yaml:"progressTransitions"`
	CheckpointBehavior         string               `yaml:"checkpointBehavior"`
	HumanGates                 []string             `yaml:"humanGates"`
	CompletionContract         []string             `yaml:"completionContract"`
}
type PlannedValidation struct {
	Name             string   `yaml:"name"`
	Steps            []string `yaml:"steps"`
	Repair           string   `yaml:"repair"`
	RepairActor      string   `yaml:"repairActor,omitempty"`
	RepairReasoning  string   `yaml:"repairReasoning,omitempty"`
	PostRepairSteps  []string `yaml:"postRepairSteps,omitempty"`
	ProducesEvidence []string `yaml:"producesEvidence,omitempty"`
}

// PlannedContracts exposes the typed handoff authority separately from actor
// prompts and runtime logs. It is empty for v1alpha1/v1alpha2 documents.
type PlannedContracts struct {
	Artifacts []PlannedArtifact `yaml:"artifacts,omitempty"`
	Evidence  []PlannedEvidence `yaml:"evidence,omitempty"`
}

// PlannedCriteria exposes runtime-owned item completion and any presentation
// adapter without making the adapter part of the executable authority.
type PlannedCriteria struct {
	Items           []WorkItem                `yaml:"items,omitempty"`
	MarkdownAdapter *MarkdownChecklistAdapter `yaml:"markdownAdapter,omitempty"`
}

type PlannedArtifact struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Paths       []string `yaml:"paths"`
	Persistence string   `yaml:"persistence"`
	Producer    string   `yaml:"producer,omitempty"`
}

type PlannedEvidence struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Validation string `yaml:"validation,omitempty"`
}

// PlannedAgent is the resolved executor contract. It exposes inherited
// defaults without exposing provider output or prompt contents.
type PlannedAgent struct {
	Name              string `yaml:"name"`
	Runner            string `yaml:"runner"`
	Model             string `yaml:"model,omitempty"`
	Sandbox           string `yaml:"sandbox,omitempty"`
	Approval          string `yaml:"approval,omitempty"`
	Ephemeral         bool   `yaml:"ephemeral"`
	Color             string `yaml:"color,omitempty"`
	MayCommit         bool   `yaml:"mayCommit"`
	OutputLastMessage bool   `yaml:"outputLastMessage"`
}
type PlannedPhase struct {
	ID              string               `yaml:"id"`
	Kind            string               `yaml:"kind"`
	Actor           string               `yaml:"actor,omitempty"`
	DependsOn       []string             `yaml:"dependsOn,omitempty"`
	Reasoning       string               `yaml:"reasoning,omitempty"`
	RequiresChange  bool                 `yaml:"requiresChange"`
	CriterionID     string               `yaml:"criterionID,omitempty"`
	AdvanceProgress bool                 `yaml:"advanceProgress"`
	Validation      string               `yaml:"validation"`
	Bookkeeping     []MarkdownTransition `yaml:"bookkeeping,omitempty"`
	Inputs          []ContractInput      `yaml:"inputs,omitempty"`
	Outputs         []string             `yaml:"outputs,omitempty"`
	IfEvidence      string               `yaml:"ifEvidence,omitempty"`
	ReadOnly        bool                 `yaml:"readOnly,omitempty"`
	WorkItemID      string               `yaml:"workItemID,omitempty"`
	AdvanceWorkItem bool                 `yaml:"advanceWorkItem,omitempty"`
	Acceptance      []string             `yaml:"acceptance"`
}

func BuildExpandedPlan(d *Document) (ExpandedPlan, error) {
	if result := Validate(d); result.Status == Invalid {
		return ExpandedPlan{}, fmt.Errorf("workflow is invalid")
	} else if result.Status == Unsupported {
		return ExpandedPlan{}, fmt.Errorf("workflow is unsupported by this runtime")
	}
	n, err := NormalizeWorkflow(d)
	if err != nil {
		return ExpandedPlan{}, err
	}
	w := n.Workflow
	resolvedLifecycle := w.Spec.Lifecycle
	if resolvedLifecycle.Policy == "" && resolvedLifecycle.Validation == "" && resolvedLifecycle.Checkpoint == "" {
		for _, phase := range w.Spec.Phases {
			if runtimeOwnsPhaseLifecycle(w, phase) {
				resolvedLifecycle.Policy = "safe-resume"
				break
			}
		}
	}
	plan := ExpandedPlan{
		Workflow:                   w.Metadata.Name,
		NormalizedExecution:        normalizedExecutionProjection(w, n.DependencyGraph),
		WorkspaceMutationAllowlist: append([]string{}, w.Spec.Workspace.MutationPolicy.Allowed...),
		DependencyGraph:            clonePhaseDependencyGraph(n.DependencyGraph),
		ResolvedLifecycle:          resolvedLifecycle,
		SafetyEnforcementPoints: []string{
			"before actor and tool work",
			"after every actor invocation, including provider errors: enforce may_commit for the invoked actor against observed HEAD",
			"after actor and tool work",
			"before checkpoint",
			"before acceptance marker reuse",
			"during interrupted-phase recovery",
		},
		RecoveryBehavior:   []string{"completed commit marker wins over stale active state", "actor_completed resumes deterministic acceptance without replaying the actor", "otherwise validate retained work before rerunning the same phase actor", "safety failures are terminal and never repaired by an actor"},
		CheckpointBehavior: "runtime checkpoints accepted allowed dirty work; its commit is runtime-owned, not an actor may_commit exercise; it rechecks lineage, integrity, scope, and cleanliness before the commit-valued phase marker",
	}
	if len(w.Spec.Criteria.Items) != 0 {
		plan.Criteria.Items = append([]WorkItem(nil), w.Spec.Criteria.Items...)
		if adapter := w.Spec.Criteria.MarkdownAdapter; adapter != nil {
			items := make(map[string]string, len(adapter.Items))
			for id, label := range adapter.Items {
				items[id] = label
			}
			plan.Criteria.MarkdownAdapter = &MarkdownChecklistAdapter{Path: adapter.Path, Items: items}
		}
	}
	for _, name := range sortedKeys(w.Spec.Agents) {
		a := w.Spec.Agents[name]
		plan.ResolvedAgents = append(plan.ResolvedAgents, PlannedAgent{
			Name: name, Runner: a.Runner, Model: a.Model, Sandbox: a.Sandbox,
			Approval: a.Approval, Ephemeral: a.Ephemeral, Color: a.Color,
			MayCommit: a.MayCommit, OutputLastMessage: a.OutputLastMessage,
		})
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
		postRepair := make([]string, 0, len(v.OnFailure.Then))
		for _, step := range v.OnFailure.Then {
			postRepair = append(postRepair, toolUseText(step))
		}
		plan.Validations = append(plan.Validations, PlannedValidation{
			Name: name, Steps: steps, Repair: repair,
			RepairActor:      v.OnFailure.Repair.Actor,
			RepairReasoning:  v.OnFailure.Repair.Reasoning,
			PostRepairSteps:  postRepair,
			ProducesEvidence: append([]string(nil), v.ProducesEvidence...),
		})
	}
	for _, name := range sortedKeys(w.Spec.Contracts.Artifacts) {
		artifact := w.Spec.Contracts.Artifacts[name]
		plan.TypedContracts.Artifacts = append(plan.TypedContracts.Artifacts, PlannedArtifact{
			Name: name, Type: artifact.Type, Paths: append([]string(nil), artifact.Paths...), Persistence: artifact.Persistence,
			Producer: contractArtifactProducer(w.Spec.Phases, name),
		})
	}
	for _, name := range sortedKeys(w.Spec.Contracts.Evidence) {
		evidence := w.Spec.Contracts.Evidence[name]
		plan.TypedContracts.Evidence = append(plan.TypedContracts.Evidence, PlannedEvidence{
			Name: name, Type: evidence.Type, Validation: contractEvidenceProducer(w.Spec.Validation, name),
		})
	}
	for phaseIndex, p := range w.Spec.Phases {
		validation := p.Validation
		if validation == "" {
			validation = w.Spec.Lifecycle.Validation
		}
		acceptance := []string{"persist active phase"}
		if p.Kind == "bookkeeping" && len(p.Bookkeeping) > 0 {
			acceptance = append(acceptance, "deterministic validation", "deterministic bookkeeping")
		} else {
			acceptance = append(acceptance, "run actor", "persist actor_completed")
			if p.Kind == "criterion" && p.AdvanceProgress {
				acceptance = append(acceptance, "assert actor left progress unchanged")
			}
			acceptance = append(acceptance, "deterministic validation")
		}
		if p.AdvanceWorkItem {
			acceptance = append(acceptance, "advance exact typed work item")
		}
		if p.Kind == "criterion" {
			acceptance = append(acceptance, "assert declared progress transition")
		}
		if p.RequiresChange {
			acceptance = append(acceptance, "assert net repository change")
		}
		acceptance = append(acceptance, "checkpoint", "write completed commit marker", "clear active phase")
		criterionID := p.CriterionID
		if criterionID == "" && p.Criterion != "" {
			for _, criterion := range w.Spec.Progress.Criteria {
				if criterion.ID == p.Criterion || criterion.Text == p.Criterion {
					criterionID = criterion.ID
					break
				}
			}
		}
		plan.Phases = append(plan.Phases, PlannedPhase{
			ID: p.ID, Kind: p.Kind, Actor: p.Actor,
			DependsOn: n.DependencyGraph.dependenciesForPhase(phaseIndex), Reasoning: p.Reasoning,
			RequiresChange: p.RequiresChange, CriterionID: criterionID,
			AdvanceProgress: p.AdvanceProgress, Validation: validation,
			Bookkeeping: p.Bookkeeping, Inputs: append([]ContractInput(nil), p.Inputs...),
			Outputs: append([]string(nil), p.Outputs...), IfEvidence: p.IfEvidence,
			ReadOnly: p.ReadOnly, WorkItemID: p.WorkItemID, AdvanceWorkItem: p.AdvanceWorkItem, Acceptance: acceptance,
		})
		if p.Kind == "criterion" && p.AdvanceProgress {
			plan.ProgressTransitions = append(plan.ProgressTransitions, p.ID+": engine advances only criterionID after validation")
		}
		if p.AdvanceWorkItem {
			plan.ProgressTransitions = append(plan.ProgressTransitions, p.ID+": engine advances only workItemID after validation")
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

func normalizedExecutionProjection(w *Workflow, graph PhaseDependencyGraph) Workflow {
	projection := *w
	projection.Spec = w.Spec
	// Defaults are authoring-only inputs. NormalizeWorkflow has already applied
	// them to the effective agents, lifecycle, phases, and repair policies.
	// Leaving them here would make it unclear whether an authority came from a
	// generated default or from the resolved execution contract.
	projection.Spec.Defaults = AuthoringDefaults{}
	projection.DependencyGraph = clonePhaseDependencyGraph(graph)
	return projection
}

func runtimeOwnsPhaseLifecycle(w *Workflow, p Phase) bool {
	lifecycle := w.Spec.Lifecycle
	if lifecycle.Policy != "" || lifecycle.Validation != "" || lifecycle.Checkpoint != "" {
		return true
	}
	if p.AdvanceProgress || p.AdvanceWorkItem || len(p.Bookkeeping) > 0 {
		return true
	}
	return len(w.Spec.PhaseDefaults.Before) == 0 &&
		len(w.Spec.PhaseDefaults.After) == 0 &&
		len(p.After) == 0
}

func toolUseText(u ToolUse) string {
	if u.If == "" {
		return u.Uses
	}
	return u.Uses + " if " + u.If
}

func contractArtifactProducer(phases []Phase, name string) string {
	for _, phase := range phases {
		for _, output := range phase.Outputs {
			if output == name {
				return phase.ID
			}
		}
	}
	return ""
}

func contractEvidenceProducer(validations map[string]Validation, name string) string {
	for _, validationName := range sortedKeys(validations) {
		for _, produced := range validations[validationName].ProducesEvidence {
			if produced == name {
				return validationName
			}
		}
	}
	return ""
}
