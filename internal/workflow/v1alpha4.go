package workflow

import (
	"fmt"
	"strings"
)

const v1alpha4APIVersion = "agentflow.dev/v1alpha4"

// V1Alpha4Workflow adds typed, runtime-owned work-item completion. Its
// bounded collection form is expanded into ordinary dependency nodes before
// execution, preserving the v1alpha2/v1alpha3 scheduler contract.
type V1Alpha4Workflow struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   V1Alpha2Metadata `yaml:"metadata"`
	Spec       V1Alpha4Spec     `yaml:"spec"`
	File       string           `yaml:"-"`
}

type V1Alpha4Spec struct {
	Parameters    map[string]Parameter          `yaml:"parameters"`
	Workspace     V1Alpha2Workspace             `yaml:"workspace"`
	Agents        map[string]V1Alpha2Agent      `yaml:"agents"`
	Tools         map[string]Tool               `yaml:"tools"`
	Preconditions []Check                       `yaml:"preconditions"`
	Validation    map[string]V1Alpha3Validation `yaml:"validation"`
	Artifacts     map[string]V1Alpha3Artifact   `yaml:"artifacts"`
	Evidence      map[string]V1Alpha3Evidence   `yaml:"evidence"`
	Criteria      V1Alpha4Criteria              `yaml:"criteria"`
	Phases        []V1Alpha4Phase               `yaml:"phases"`
	HumanGates    []HumanGate                   `yaml:"humanGates"`
	Completion    V1Alpha3Completion            `yaml:"completion"`
	Reset         V1Alpha2Reset                 `yaml:"reset"`
}

type V1Alpha4Criteria struct {
	Items             []WorkItem                        `yaml:"items"`
	MarkdownChecklist *V1Alpha4MarkdownChecklistAdapter `yaml:"markdownChecklist"`
}

// V1Alpha4MarkdownChecklistAdapter is intentionally an adapter: its bytes
// mirror runtime-owned work-item state, never determine it.
type V1Alpha4MarkdownChecklistAdapter struct {
	Path  string            `yaml:"path"`
	Items map[string]string `yaml:"items"`
}

type V1Alpha4Phase struct {
	ID              string                  `yaml:"id"`
	Kind            string                  `yaml:"kind"`
	Actor           V1Alpha2Actor           `yaml:"actor"`
	Prompt          string                  `yaml:"prompt"`
	Reasoning       string                  `yaml:"reasoning"`
	RequiresChange  *bool                   `yaml:"requiresChange"`
	If              string                  `yaml:"if"`
	IfEvidence      string                  `yaml:"ifEvidence"`
	Validation      string                  `yaml:"validation"`
	DependsOn       []string                `yaml:"dependsOn"`
	Inputs          []V1Alpha3ContractInput `yaml:"inputs"`
	Outputs         []string                `yaml:"outputs"`
	ReadOnly        bool                    `yaml:"readOnly"`
	WorkItem        string                  `yaml:"workItem"`
	AdvanceWorkItem bool                    `yaml:"advanceWorkItem"`
	ForEach         *V1Alpha4ForEach        `yaml:"forEach"`
}

// V1Alpha4ForEach explicitly bounds a static collection expansion. A future
// dynamic collection feature must introduce its own execution semantics; this
// form cannot repeatedly select or rediscover items at runtime.
type V1Alpha4ForEach struct {
	WorkItems []string `yaml:"workItems"`
	MaxItems  int      `yaml:"maxItems"`
}

type v1alpha4ExpandedPhase struct {
	phase       V1Alpha3Phase
	workItemID  string
	advanceItem bool
}

func (w *V1Alpha4Workflow) v1alpha3Projection() (*V1Alpha3Workflow, []v1alpha4ExpandedPhase) {
	expanded := w.expandedPhases()
	phases := make([]V1Alpha3Phase, len(expanded))
	for i, item := range expanded {
		phases[i] = item.phase
	}
	return &V1Alpha3Workflow{
		APIVersion: w.APIVersion, Kind: w.Kind, Metadata: w.Metadata,
		Spec: V1Alpha3Spec{
			Parameters: w.Spec.Parameters, Workspace: w.Spec.Workspace, Agents: w.Spec.Agents,
			Tools: w.Spec.Tools, Preconditions: w.Spec.Preconditions, Validation: w.Spec.Validation,
			Artifacts: w.Spec.Artifacts, Evidence: w.Spec.Evidence, Phases: phases,
			HumanGates: w.Spec.HumanGates, Completion: w.Spec.Completion, Reset: w.Spec.Reset,
		}, File: w.File,
	}, expanded
}

func (w *V1Alpha4Workflow) expandedPhases() []v1alpha4ExpandedPhase {
	descriptions := make(map[string]string, len(w.Spec.Criteria.Items))
	for _, item := range w.Spec.Criteria.Items {
		descriptions[item.ID] = item.Description
	}
	phaseIDs := make(map[string][]string, len(w.Spec.Phases))
	for _, phase := range w.Spec.Phases {
		if phase.ForEach == nil {
			phaseIDs[phase.ID] = []string{phase.ID}
			continue
		}
		ids := make([]string, 0, len(phase.ForEach.WorkItems))
		for _, itemID := range phase.ForEach.WorkItems {
			ids = append(ids, phase.ID+"--"+itemID)
		}
		phaseIDs[phase.ID] = ids
	}

	expanded := make([]v1alpha4ExpandedPhase, 0, len(w.Spec.Phases))
	for _, phase := range w.Spec.Phases {
		workItems := []string{phase.WorkItem}
		if phase.ForEach != nil {
			workItems = phase.ForEach.WorkItems
		}
		for _, itemID := range workItems {
			id := phase.ID
			if phase.ForEach != nil {
				id += "--" + itemID
			}
			dependsOn := make([]string, 0, len(phase.DependsOn))
			for _, dependency := range phase.DependsOn {
				dependsOn = append(dependsOn, phaseIDs[dependency]...)
			}
			prompt := phase.Prompt
			if itemID != "" {
				prompt += fmt.Sprintf("\n\nAssigned work item %q: %s", itemID, descriptions[itemID])
			}
			expanded = append(expanded, v1alpha4ExpandedPhase{
				phase: V1Alpha3Phase{
					ID: id, Kind: phase.Kind, Actor: phase.Actor, Prompt: prompt, Reasoning: phase.Reasoning,
					RequiresChange: phase.RequiresChange, If: phase.If, IfEvidence: phase.IfEvidence,
					Validation: phase.Validation, DependsOn: dependsOn, Inputs: append([]V1Alpha3ContractInput(nil), phase.Inputs...),
					Outputs: append([]string(nil), phase.Outputs...), ReadOnly: phase.ReadOnly,
				},
				workItemID: itemID, advanceItem: phase.AdvanceWorkItem,
			})
		}
	}
	return expanded
}

func normalizeV1Alpha4(authored *V1Alpha4Workflow, locations Locations) (*Document, error) {
	if authored == nil {
		return nil, fmt.Errorf("empty v1alpha4 workflow")
	}
	projected, expanded := authored.v1alpha3Projection()
	normalized, err := normalizeV1Alpha3(projected, locations)
	if err != nil {
		return nil, err
	}
	w := normalized.Workflow
	w.APIVersion = authored.APIVersion
	w.Spec.Criteria.Items = append([]WorkItem(nil), authored.Spec.Criteria.Items...)
	if adapter := authored.Spec.Criteria.MarkdownChecklist; adapter != nil {
		items := make(map[string]string, len(adapter.Items))
		for id, item := range adapter.Items {
			items[id] = item
		}
		w.Spec.Criteria.MarkdownAdapter = &MarkdownChecklistAdapter{Path: adapter.Path, Items: items}
	}
	for i, phase := range expanded {
		w.Spec.Phases[i].WorkItemID = phase.workItemID
		w.Spec.Phases[i].AdvanceWorkItem = phase.advanceItem
	}
	normalized.V1Alpha3 = nil
	normalized.V1Alpha4 = authored
	return normalized, nil
}

func validateV1Alpha4(d *Document) Result {
	if d == nil || d.V1Alpha4 == nil {
		return Result{Status: Invalid, Document: d, Diagnostics: []Diagnostic{{Status: Invalid, Message: "empty v1alpha4 workflow document"}}}
	}
	projected, _ := d.V1Alpha4.v1alpha3Projection()
	projected.APIVersion = v1alpha3APIVersion
	projectedDocument, err := normalizeV1Alpha3(projected, d.Locations)
	if err != nil {
		return Result{Status: Invalid, Document: d, Diagnostics: []Diagnostic{{Status: Invalid, Message: fmt.Sprintf("normalize v1alpha4 workflow: %v", err)}}}
	}
	base := validateV1Alpha3(&Document{Workflow: projectedDocument.Workflow, V1Alpha3: projected, Locations: d.Locations})
	result := Result{Status: base.Status, Document: d, Normalized: d, Diagnostics: append([]Diagnostic(nil), base.Diagnostics...)}
	validator := v1alpha4Validator{result: &result, locations: d.Locations, authored: d.V1Alpha4}
	validator.criteria()
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Status == Invalid {
			result.Status = Invalid
			return result
		}
	}
	return result
}

type v1alpha4Validator struct {
	result    *Result
	locations Locations
	authored  *V1Alpha4Workflow
}

func (v v1alpha4Validator) add(path, format string, args ...any) {
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{
		Status: Invalid, Path: path, Position: v.locations[path], Message: fmt.Sprintf(format, args...),
	})
}

func (v v1alpha4Validator) criteria() {
	items := map[string]bool{}
	for index, item := range v.authored.Spec.Criteria.Items {
		path := fmt.Sprintf("spec.criteria.items[%d]", index)
		if !identifierPattern.MatchString(item.ID) {
			v.add(path+".id", "work item id %q must match %s", item.ID, identifierPattern.String())
		}
		if strings.TrimSpace(item.Description) == "" {
			v.add(path+".description", "is required")
		}
		if items[item.ID] {
			v.add(path+".id", "duplicate work item id %q", item.ID)
		}
		items[item.ID] = true
	}
	if len(items) == 0 {
		v.add("spec.criteria.items", "must declare at least one work item")
	}
	if adapter := v.authored.Spec.Criteria.MarkdownChecklist; adapter != nil {
		if !workspaceRelative(adapter.Path) {
			v.add("spec.criteria.markdownChecklist.path", "must be workspace-relative")
		}
		for id, label := range adapter.Items {
			if !items[id] {
				v.add("spec.criteria.markdownChecklist.items."+id, "references unknown work item %q", id)
			}
			if strings.TrimSpace(label) == "" {
				v.add("spec.criteria.markdownChecklist.items."+id, "must not be empty")
			}
		}
		for id := range items {
			if _, ok := adapter.Items[id]; !ok {
				v.add("spec.criteria.markdownChecklist.items", "must map work item %q", id)
			}
		}
	}

	advancers := map[string]string{}
	for index, phase := range v.authored.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", index)
		if phase.AdvanceWorkItem && phase.If != "" {
			v.add(path+".if", "work-item advancement phases cannot be conditional")
		}
		if phase.AdvanceWorkItem && phase.IfEvidence != "" {
			v.add(path+".ifEvidence", "work-item advancement phases cannot be conditional")
		}
		if phase.ForEach != nil {
			if phase.WorkItem != "" {
				v.add(path+".workItem", "cannot be combined with forEach")
			}
			if !phase.AdvanceWorkItem {
				v.add(path+".advanceWorkItem", "forEach phases must declare true")
			}
			if phase.ForEach.MaxItems <= 0 {
				v.add(path+".forEach.maxItems", "must be positive")
			}
			if phase.ForEach.MaxItems != len(phase.ForEach.WorkItems) {
				v.add(path+".forEach.maxItems", "must equal the declared workItems count, making expansion statically bounded")
			}
			if len(phase.Outputs) != 0 {
				v.add(path+".outputs", "forEach phases cannot emit a shared artifact")
			}
			local := map[string]bool{}
			for itemIndex, id := range phase.ForEach.WorkItems {
				itemPath := fmt.Sprintf("%s.forEach.workItems[%d]", path, itemIndex)
				if !items[id] {
					v.add(itemPath, "references unknown work item %q", id)
				}
				if local[id] {
					v.add(itemPath, "duplicates work item %q", id)
				}
				local[id] = true
				v.registerAdvancer(advancers, itemPath, id, phase.ID+"--"+id)
			}
			continue
		}
		if phase.WorkItem == "" && phase.AdvanceWorkItem {
			v.add(path+".advanceWorkItem", "requires workItem")
		}
		if phase.WorkItem != "" {
			if !items[phase.WorkItem] {
				v.add(path+".workItem", "references unknown work item %q", phase.WorkItem)
			}
			if !phase.AdvanceWorkItem {
				v.add(path+".advanceWorkItem", "work item phases must declare true")
			}
			v.registerAdvancer(advancers, path+".workItem", phase.WorkItem, phase.ID)
		}
	}
	for id := range items {
		if _, ok := advancers[id]; !ok {
			v.add("spec.criteria.items", "work item %q has no exact-target advancement phase", id)
		}
	}
}

func (v v1alpha4Validator) registerAdvancer(advancers map[string]string, path, itemID, phaseID string) {
	if prior, exists := advancers[itemID]; exists {
		v.add(path, "work item %q is already advanced by phase %q", itemID, prior)
		return
	}
	advancers[itemID] = phaseID
}
