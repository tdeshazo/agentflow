package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const contractRecordVersion = 1

// ContractArtifact is durable proof that a producer phase emitted the exact
// declared workspace content. It excludes actor prose, logs, and parameter
// values; consumers receive only the named identity they declared.
type ContractArtifact struct {
	Version     int                    `json:"version"`
	Name        string                 `json:"name"`
	Producer    string                 `json:"producer"`
	Type        string                 `json:"type"`
	Persistence string                 `json:"persistence"`
	Files       []contractArtifactFile `json:"files"`
}

type contractArtifactFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Mode   uint32 `json:"mode"`
}

// ContractEvidence connects a named claim to one successful deterministic
// validation in a specific accepted producer phase. It is deliberately a
// separate record from cache evidence: reuse remains an optimization while a
// contract evidence record is a handoff authorization.
type ContractEvidence struct {
	Version    int    `json:"version"`
	Name       string `json:"name"`
	Producer   string `json:"producer"`
	Validation string `json:"validation"`
}

func (e *Engine) contractArtifactRecord(phaseID, name string) string {
	return "contracts/artifacts/" + phaseID + "/" + name
}

func (e *Engine) contractEvidenceRecord(phaseID, name string) string {
	return "contracts/evidence/" + phaseID + "/" + name
}

func (e *Engine) persistPhaseContractOutputs(phase *workflow.Phase) error {
	if len(phase.Outputs) == 0 {
		return nil
	}
	for _, name := range phase.Outputs {
		artifact, ok := e.Workflow.Spec.Contracts.Artifacts[name]
		if !ok {
			return fmt.Errorf("phase %s emits unknown artifact %q", phase.ID, name)
		}
		captured, err := e.captureContractArtifact(phase, name, artifact)
		if err != nil {
			return err
		}
		if err := e.Store.SetJSON(e.contractArtifactRecord(phase.ID, name), captured); err != nil {
			return fmt.Errorf("persist artifact %q from phase %s: %w", name, phase.ID, err)
		}
	}
	return nil
}

func (e *Engine) captureContractArtifact(phase *workflow.Phase, name string, artifact workflow.Artifact) (ContractArtifact, error) {
	files, err := e.Repo.PresentFiles()
	if err != nil {
		return ContractArtifact{}, err
	}
	matched := map[string]bool{}
	for _, declared := range artifact.Paths {
		pattern, err := e.context(phase).Expand(declared)
		if err != nil {
			return ContractArtifact{}, fmt.Errorf("expand artifact %q path: %w", name, err)
		}
		pattern, err = workspaceRelativePattern(e.Repo.Root, pattern)
		if err != nil {
			return ContractArtifact{}, fmt.Errorf("artifact %q path: %w", name, err)
		}
		found := false
		for _, file := range files {
			if !dependencyMatches(pattern, file) {
				continue
			}
			found = true
			matched[file] = true
		}
		if !found {
			return ContractArtifact{}, fmt.Errorf("artifact %q producer phase %s did not produce declared path %q", name, phase.ID, declared)
		}
	}
	paths := make([]string, 0, len(matched))
	for path := range matched {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	record := ContractArtifact{Version: contractRecordVersion, Name: name, Producer: phase.ID, Type: artifact.Type, Persistence: artifact.Persistence, Files: make([]contractArtifactFile, 0, len(paths))}
	for _, path := range paths {
		identity, err := hashValidationFile(filepath.Join(e.Repo.Root, filepath.FromSlash(path)))
		if err != nil {
			return ContractArtifact{}, err
		}
		record.Files = append(record.Files, contractArtifactFile{Path: path, Digest: identity.Digest, Mode: identity.Mode})
	}
	return record, nil
}

func (e *Engine) validateContractInputs(phase *workflow.Phase) error {
	for _, input := range phase.Inputs {
		if input.Artifact != "" {
			if err := e.validateContractArtifactInput(phase, input.Artifact); err != nil {
				return err
			}
		}
		if input.Evidence != "" {
			available, err := e.contractEvidenceAvailable(phase, input.Evidence)
			if err != nil {
				return err
			}
			if !available {
				return fmt.Errorf("phase %s requires evidence %q, but it is unavailable", phase.ID, input.Evidence)
			}
		}
	}
	return nil
}

func (e *Engine) validateContractArtifactInput(consumer *workflow.Phase, name string) error {
	_, err := e.loadContractArtifactInput(consumer, name)
	return err
}

func (e *Engine) loadContractArtifactInput(consumer *workflow.Phase, name string) (ContractArtifact, error) {
	producer, err := e.contractArtifactProducer(consumer, name)
	if err != nil {
		return ContractArtifact{}, err
	}
	var record ContractArtifact
	ok, err := e.Store.GetJSON(e.contractArtifactRecord(producer.ID, name), &record)
	if err != nil {
		return ContractArtifact{}, err
	}
	if !ok {
		return ContractArtifact{}, fmt.Errorf("phase %s requires artifact %q from phase %s, but no durable artifact record exists", consumer.ID, name, producer.ID)
	}
	declared, declaredOK := e.Workflow.Spec.Contracts.Artifacts[name]
	if !declaredOK || record.Version != contractRecordVersion || record.Name != name || record.Producer != producer.ID || record.Type != declared.Type || record.Persistence != declared.Persistence || len(record.Files) == 0 {
		return ContractArtifact{}, fmt.Errorf("phase %s requires compatible artifact %q from phase %s", consumer.ID, name, producer.ID)
	}
	patterns := make([]string, 0, len(declared.Paths))
	for _, path := range declared.Paths {
		expanded, err := e.context(consumer).Expand(path)
		if err != nil {
			return ContractArtifact{}, fmt.Errorf("expand artifact %q input path: %w", name, err)
		}
		pattern, err := workspaceRelativePattern(e.Repo.Root, expanded)
		if err != nil {
			return ContractArtifact{}, fmt.Errorf("artifact %q input path: %w", name, err)
		}
		patterns = append(patterns, pattern)
	}
	matchedPatterns := make([]bool, len(patterns))
	priorPath := ""
	for _, file := range record.Files {
		safe, err := workspaceRelativePattern(e.Repo.Root, file.Path)
		if err != nil || safe != filepath.ToSlash(file.Path) || priorPath != "" && file.Path <= priorPath {
			return ContractArtifact{}, fmt.Errorf("phase %s requires compatible artifact %q from phase %s", consumer.ID, name, producer.ID)
		}
		priorPath = file.Path
		declaredFile := false
		for i, pattern := range patterns {
			if dependencyMatches(pattern, file.Path) {
				declaredFile = true
				matchedPatterns[i] = true
			}
		}
		if !declaredFile {
			return ContractArtifact{}, fmt.Errorf("phase %s artifact %q record contains undeclared path %q", consumer.ID, name, file.Path)
		}
		identity, err := hashValidationFile(filepath.Join(e.Repo.Root, filepath.FromSlash(file.Path)))
		if err != nil {
			return ContractArtifact{}, fmt.Errorf("phase %s artifact %q input %q is missing: %w", consumer.ID, name, file.Path, err)
		}
		if identity.Digest != file.Digest || identity.Mode != file.Mode {
			return ContractArtifact{}, fmt.Errorf("phase %s artifact %q input %q no longer matches producer identity", consumer.ID, name, file.Path)
		}
	}
	for i, matched := range matchedPatterns {
		if !matched {
			return ContractArtifact{}, fmt.Errorf("phase %s artifact %q record omits declared path %q", consumer.ID, name, declared.Paths[i])
		}
	}
	return record, nil
}

func (e *Engine) contractArtifactProducer(consumer *workflow.Phase, name string) (*workflow.Phase, error) {
	for _, dependency := range e.Workflow.DependencyGraph.Dependencies(consumer.ID) {
		phase, err := e.phaseByID(dependency)
		if err != nil {
			return nil, err
		}
		for _, output := range phase.Outputs {
			if output == name {
				return phase, nil
			}
		}
	}
	return nil, fmt.Errorf("phase %s has no dependency that produces artifact %q", consumer.ID, name)
}

func (e *Engine) persistContractValidationEvidence(name string, phase *workflow.Phase) error {
	if phase == nil {
		return nil
	}
	validation, ok := e.Workflow.Spec.Validation[name]
	if !ok {
		return fmt.Errorf("unknown validation %q", name)
	}
	for _, evidence := range validation.ProducesEvidence {
		record := ContractEvidence{Version: contractRecordVersion, Name: evidence, Producer: phase.ID, Validation: name}
		if err := e.Store.SetJSON(e.contractEvidenceRecord(phase.ID, evidence), record); err != nil {
			return fmt.Errorf("persist evidence %q from phase %s: %w", evidence, phase.ID, err)
		}
	}
	return nil
}

func (e *Engine) contractEvidenceAvailable(consumer *workflow.Phase, name string) (bool, error) {
	if _, ok := e.Workflow.Spec.Contracts.Evidence[name]; !ok {
		return false, fmt.Errorf("phase %s requires undeclared evidence %q", consumer.ID, name)
	}
	for _, dependency := range e.Workflow.DependencyGraph.Dependencies(consumer.ID) {
		phase, err := e.phaseByID(dependency)
		if err != nil {
			return false, err
		}
		validationName := e.phaseValidation(phase)
		validation, ok := e.Workflow.Spec.Validation[validationName]
		if !ok {
			continue
		}
		for _, produced := range validation.ProducesEvidence {
			if produced != name {
				continue
			}
			var record ContractEvidence
			ok, err := e.Store.GetJSON(e.contractEvidenceRecord(phase.ID, name), &record)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
			if record.Version != contractRecordVersion || record.Name != name || record.Producer != phase.ID || record.Validation != validationName {
				return false, fmt.Errorf("phase %s requires compatible evidence %q from phase %s", consumer.ID, name, phase.ID)
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("phase %s has no dependency that produces evidence %q", consumer.ID, name)
}

func (e *Engine) requireCompletionEvidence() error {
	completion := e.Workflow.Spec.Completion["default"]
	for _, name := range completion.Evidence {
		if _, ok := e.Workflow.Spec.Contracts.Evidence[name]; !ok {
			return fmt.Errorf("completion requires undeclared evidence %q", name)
		}
		found := false
		for _, phase := range e.Workflow.Spec.Phases {
			validationName := e.phaseValidation(&phase)
			validation, ok := e.Workflow.Spec.Validation[validationName]
			if !ok {
				continue
			}
			for _, produced := range validation.ProducesEvidence {
				if produced != name {
					continue
				}
				var record ContractEvidence
				ok, err := e.Store.GetJSON(e.contractEvidenceRecord(phase.ID, name), &record)
				if err != nil {
					return err
				}
				if ok && record.Version == contractRecordVersion && record.Name == name && record.Producer == phase.ID && record.Validation == validationName {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("completion requires evidence %q, but no compatible deterministic evidence is available", name)
		}
	}
	return nil
}

func (e *Engine) assertReadOnlyPhase(phase *workflow.Phase, start string) error {
	if !phase.ReadOnly {
		return nil
	}
	files, err := e.Repo.ChangedFilesSince(start)
	if err != nil {
		return err
	}
	if files = e.filterIgnored(files); len(files) > 0 {
		return fmt.Errorf("read-only audit phase %s changed workspace paths: %v", phase.ID, files)
	}
	return nil
}
