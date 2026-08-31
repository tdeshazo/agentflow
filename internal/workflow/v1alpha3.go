package workflow

import (
	"fmt"

	"github.com/tdeshazo/agentflow/internal/workspacepath"
)

const v1alpha3APIVersion = "agentflow.dev/v1alpha3"

// V1Alpha3Workflow adds typed handoffs without changing the v1alpha2
// contract. Its fields lower into the shared executable Workflow so the
// runtime retains one safety, lifecycle, and scheduling implementation.
type V1Alpha3Workflow struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   V1Alpha2Metadata `yaml:"metadata"`
	Spec       V1Alpha3Spec     `yaml:"spec"`
	File       string           `yaml:"-"`
}

type V1Alpha3Spec struct {
	Parameters    map[string]Parameter          `yaml:"parameters"`
	Workspace     V1Alpha2Workspace             `yaml:"workspace"`
	Agents        map[string]V1Alpha2Agent      `yaml:"agents"`
	Tools         map[string]Tool               `yaml:"tools"`
	Preconditions []Check                       `yaml:"preconditions"`
	Validation    map[string]V1Alpha3Validation `yaml:"validation"`
	Artifacts     map[string]V1Alpha3Artifact   `yaml:"artifacts"`
	Evidence      map[string]V1Alpha3Evidence   `yaml:"evidence"`
	Phases        []V1Alpha3Phase               `yaml:"phases"`
	HumanGates    []V1Alpha2HumanGate           `yaml:"humanGates"`
	Completion    V1Alpha3Completion            `yaml:"completion"`
	Reset         V1Alpha2Reset                 `yaml:"reset"`
}

type V1Alpha3Validation struct {
	Run          string               `yaml:"run"`
	Steps        []ToolUse            `yaml:"steps"`
	Dependencies []string             `yaml:"dependencies"`
	Hard         bool                 `yaml:"hard"`
	Repair       V1Alpha2RepairPolicy `yaml:"repair"`
	Produces     []string             `yaml:"produces"`
}

type V1Alpha3Artifact struct {
	Type        string   `yaml:"type"`
	Paths       []string `yaml:"paths"`
	Persistence string   `yaml:"persistence"`
}

type V1Alpha3Evidence struct {
	Type string `yaml:"type"`
}

type V1Alpha3ContractInput struct {
	Artifact string `yaml:"artifact"`
	Evidence string `yaml:"evidence"`
}

type V1Alpha3Phase struct {
	ID             string                  `yaml:"id"`
	Kind           string                  `yaml:"kind"`
	Actor          V1Alpha2Actor           `yaml:"actor"`
	Prompt         string                  `yaml:"prompt"`
	Reasoning      string                  `yaml:"reasoning"`
	RequiresChange *bool                   `yaml:"requiresChange"`
	If             string                  `yaml:"if"`
	IfEvidence     string                  `yaml:"ifEvidence"`
	Validation     string                  `yaml:"validation"`
	DependsOn      []string                `yaml:"dependsOn"`
	Inputs         []V1Alpha3ContractInput `yaml:"inputs"`
	Outputs        []string                `yaml:"outputs"`
	ReadOnly       bool                    `yaml:"readOnly"`
}

type V1Alpha3Completion struct {
	Validation string      `yaml:"validation"`
	Assertions []Assertion `yaml:"assertions"`
	Evidence   []string    `yaml:"evidence"`
}

func (w *V1Alpha3Workflow) v1alpha2Projection() *V1Alpha2Workflow {
	validations := make(map[string]V1Alpha2Validation, len(w.Spec.Validation))
	for name, validation := range w.Spec.Validation {
		validations[name] = V1Alpha2Validation{
			Run: validation.Run, Steps: append([]ToolUse(nil), validation.Steps...),
			Dependencies: append([]string(nil), validation.Dependencies...), Hard: validation.Hard,
			Repair: validation.Repair,
		}
	}
	phases := make([]V1Alpha2Phase, 0, len(w.Spec.Phases))
	for _, phase := range w.Spec.Phases {
		phases = append(phases, V1Alpha2Phase{
			ID: phase.ID, Kind: phase.Kind, Actor: phase.Actor, Prompt: phase.Prompt,
			Reasoning: phase.Reasoning, RequiresChange: phase.RequiresChange, If: phase.If,
			Validation: phase.Validation, DependsOn: append([]string(nil), phase.DependsOn...),
		})
	}
	return &V1Alpha2Workflow{
		APIVersion: w.APIVersion, Kind: w.Kind, Metadata: w.Metadata,
		Spec: V1Alpha2Spec{
			Parameters: w.Spec.Parameters, Workspace: w.Spec.Workspace, Agents: w.Spec.Agents,
			Tools: w.Spec.Tools, Preconditions: w.Spec.Preconditions, Validation: validations,
			Phases: phases, HumanGates: w.Spec.HumanGates,
			Completion: V1Alpha2Completion{Validation: w.Spec.Completion.Validation, Assertions: w.Spec.Completion.Assertions},
			Reset:      w.Spec.Reset,
		}, File: w.File,
	}
}

func normalizeV1Alpha3(authored *V1Alpha3Workflow, locations Locations) (*Document, error) {
	if authored == nil {
		return nil, fmt.Errorf("empty v1alpha3 workflow")
	}
	projected := authored.v1alpha2Projection()
	normalized, err := normalizeV1Alpha2(projected, locations)
	if err != nil {
		return nil, err
	}
	w := normalized.Workflow
	w.APIVersion = authored.APIVersion
	w.Spec.Contracts = ContractSpec{
		Artifacts: make(map[string]Artifact, len(authored.Spec.Artifacts)),
		Evidence:  make(map[string]Evidence, len(authored.Spec.Evidence)),
	}
	for name, artifact := range authored.Spec.Artifacts {
		w.Spec.Contracts.Artifacts[name] = Artifact{Type: artifact.Type, Paths: append([]string(nil), artifact.Paths...), Persistence: artifact.Persistence}
	}
	for name, evidence := range authored.Spec.Evidence {
		w.Spec.Contracts.Evidence[name] = Evidence{Type: evidence.Type}
	}
	for index, phase := range authored.Spec.Phases {
		inputs := make([]ContractInput, 0, len(phase.Inputs))
		for _, input := range phase.Inputs {
			inputs = append(inputs, ContractInput{Artifact: input.Artifact, Evidence: input.Evidence})
		}
		w.Spec.Phases[index].Inputs = inputs
		w.Spec.Phases[index].Outputs = append([]string(nil), phase.Outputs...)
		w.Spec.Phases[index].IfEvidence = phase.IfEvidence
		w.Spec.Phases[index].ReadOnly = phase.ReadOnly
	}
	for name, validation := range authored.Spec.Validation {
		shared := w.Spec.Validation[name]
		shared.ProducesEvidence = append([]string(nil), validation.Produces...)
		w.Spec.Validation[name] = shared
	}
	completion := w.Spec.Completion["default"]
	completion.Evidence = append([]string(nil), authored.Spec.Completion.Evidence...)
	w.Spec.Completion["default"] = completion
	normalized.V1Alpha3 = authored
	return normalized, nil
}

func validateV1Alpha3(d *Document) Result {
	if d == nil || d.V1Alpha3 == nil {
		return Result{Status: Invalid, Document: d, Diagnostics: []Diagnostic{{Status: Invalid, Message: "empty v1alpha3 workflow document"}}}
	}
	projected := d.V1Alpha3.v1alpha2Projection()
	// Reuse v1alpha2's shared safety/reference validator for the unchanged
	// surface. Its version check is intentionally version-specific, so present
	// the projection as v1alpha2 while validating that common subset.
	projected.APIVersion = v1alpha2APIVersion
	projectedDocument := &Document{V1Alpha2: projected, Locations: d.Locations}
	base := validateV1Alpha2(projectedDocument)
	result := Result{Status: base.Status, Document: d, Normalized: d, Diagnostics: append([]Diagnostic(nil), base.Diagnostics...)}
	validator := v1alpha3Validator{result: &result, locations: d.Locations, authored: d.V1Alpha3, workflow: d.Workflow}
	validator.contracts()
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Status == Invalid {
			result.Status = Invalid
			return result
		}
	}
	return result
}

type v1alpha3Validator struct {
	result    *Result
	locations Locations
	authored  *V1Alpha3Workflow
	workflow  *Workflow
}

func (v v1alpha3Validator) add(path, format string, args ...any) {
	position := v.locations[path]
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{Status: Invalid, Path: path, Position: position, Message: fmt.Sprintf(format, args...)})
}

func (v v1alpha3Validator) contracts() {
	if v.workflow == nil {
		return
	}
	artifactProducer := map[string]string{}
	evidenceProducer := map[string]string{}
	for name, artifact := range v.authored.Spec.Artifacts {
		path := "spec.artifacts." + name
		if !identifierPattern.MatchString(name) {
			v.add(path, "artifact name %q must match %s", name, identifierPattern.String())
		}
		if artifact.Type != "files" {
			v.add(path+".type", "must be files")
		}
		if artifact.Persistence != "workspace" {
			v.add(path+".persistence", "must be workspace")
		}
		if len(artifact.Paths) == 0 {
			v.add(path+".paths", "must declare at least one workspace-relative path")
		}
		for i, value := range artifact.Paths {
			if !workspaceRelative(value) {
				v.add(fmt.Sprintf("%s.paths[%d]", path, i), "must be workspace-relative")
			}
		}
	}
	for name, evidence := range v.authored.Spec.Evidence {
		path := "spec.evidence." + name
		if !identifierPattern.MatchString(name) {
			v.add(path, "evidence name %q must match %s", name, identifierPattern.String())
		}
		if evidence.Type != "validation" {
			v.add(path+".type", "must be validation")
		}
	}
	for name, validation := range v.authored.Spec.Validation {
		for i, evidence := range validation.Produces {
			path := fmt.Sprintf("spec.validation.%s.produces[%d]", name, i)
			if _, ok := v.authored.Spec.Evidence[evidence]; !ok {
				v.add(path, "unknown evidence %q", evidence)
				continue
			}
			if prior, exists := evidenceProducer[evidence]; exists {
				v.add(path, "evidence %q is already produced by validation %q", evidence, prior)
				continue
			}
			evidenceProducer[evidence] = name
		}
	}
	for index, phase := range v.authored.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", index)
		if phase.ReadOnly && phase.Kind != "audit" {
			v.add(path+".readOnly", "is supported only for audit phases")
		}
		if phase.Kind == "audit" && !phase.ReadOnly {
			v.add(path+".readOnly", "audit phases must declare readOnly: true")
		}
		if phase.ReadOnly && phase.RequiresChange != nil && *phase.RequiresChange {
			v.add(path+".requiresChange", "read-only phases must not require a change")
		}
		if phase.ReadOnly && len(phase.Outputs) != 0 {
			v.add(path+".outputs", "read-only phases must not emit workspace artifacts")
		}
		if phase.ReadOnly && phase.Actor.Inline == nil {
			if agent, ok := v.authored.Spec.Agents[phase.Actor.Name]; ok && agent.MayCommit {
				v.add(path+".actor", "read-only phase actor %q must not have may_commit", phase.Actor.Name)
			}
		}
		if phase.ReadOnly && phase.Actor.Inline != nil && phase.Actor.Inline.MayCommit {
			v.add(path+".actor.may_commit", "read-only phase actor must not have may_commit")
		}
		for outputIndex, artifact := range phase.Outputs {
			outputPath := fmt.Sprintf("%s.outputs[%d]", path, outputIndex)
			if _, ok := v.authored.Spec.Artifacts[artifact]; !ok {
				v.add(outputPath, "unknown artifact %q", artifact)
				continue
			}
			if prior, exists := artifactProducer[artifact]; exists {
				v.add(outputPath, "artifact %q is already produced by phase %q", artifact, prior)
				continue
			}
			artifactProducer[artifact] = phase.ID
		}
	}
	for index, phase := range v.authored.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", index)
		dependencies := stringSet(phase.DependsOn)
		for inputIndex, input := range phase.Inputs {
			inputPath := fmt.Sprintf("%s.inputs[%d]", path, inputIndex)
			hasArtifact := input.Artifact != ""
			hasEvidence := input.Evidence != ""
			if hasArtifact == hasEvidence {
				v.add(inputPath, "must reference exactly one artifact or evidence")
				continue
			}
			if hasArtifact {
				producer, exists := artifactProducer[input.Artifact]
				if !exists {
					v.add(inputPath+".artifact", "artifact %q has no phase producer", input.Artifact)
				} else if !dependencies[producer] {
					v.add(inputPath+".artifact", "artifact %q requires dependsOn: %q", input.Artifact, producer)
				}
			}
			if hasEvidence {
				validation, exists := evidenceProducer[input.Evidence]
				if !exists {
					v.add(inputPath+".evidence", "evidence %q has no validation producer", input.Evidence)
				} else if !dependencyRunsValidation(v.authored.Spec.Phases, dependencies, validation) {
					v.add(inputPath+".evidence", "evidence %q requires dependsOn phase validated by %q", input.Evidence, validation)
				}
			}
		}
		if phase.IfEvidence != "" {
			validation, exists := evidenceProducer[phase.IfEvidence]
			if !exists {
				v.add(path+".ifEvidence", "evidence %q has no validation producer", phase.IfEvidence)
			} else if !dependencyRunsValidation(v.authored.Spec.Phases, dependencies, validation) {
				v.add(path+".ifEvidence", "evidence %q requires dependsOn phase validated by %q", phase.IfEvidence, validation)
			}
		}
	}
	for i, evidence := range v.authored.Spec.Completion.Evidence {
		path := fmt.Sprintf("spec.completion.evidence[%d]", i)
		if _, exists := evidenceProducer[evidence]; !exists {
			v.add(path, "evidence %q has no validation producer", evidence)
		}
	}
}

func workspaceRelative(value string) bool {
	_, ok := workspacepath.Clean(value)
	return ok
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func dependencyRunsValidation(phases []V1Alpha3Phase, dependencies map[string]bool, validation string) bool {
	for _, phase := range phases {
		if dependencies[phase.ID] && phase.Validation == validation {
			return true
		}
	}
	return false
}
