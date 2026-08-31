package engine

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

const (
	invocationRolePhase       = "phase"
	invocationRolePhaseResume = "phase-resume"
	invocationRoleRepair      = "validation-repair"
)

// compileInvocationContext derives the complete provider-visible context from
// normalized workflow authority and current durable/workspace state. The
// result is intentionally not persisted.
func (e *Engine) compileInvocationContext(actorName, role, objective string, agent workflow.Agent, phase *workflow.Phase, validations []string) (provider.InvocationContext, error) {
	context := provider.InvocationContext{
		Version: provider.InvocationContextVersion,
		Invocation: provider.InvocationIdentity{
			Role:  role,
			Actor: actorName,
		},
		Objective:    remapWorkspacePathReferences(objective, e.Repo.Root, provider.WorkspacePlaceholder),
		Workspace:    provider.WorkspaceContext{Root: provider.WorkspacePlaceholder},
		Dependencies: []provider.DependencyContext{},
		Artifacts:    []provider.ArtifactReference{},
		Evidence:     []provider.EvidenceReference{},
		Authority: provider.InvocationAuthority{
			WritablePaths: []string{}, Protected: []provider.ProtectedPath{}, RuntimeOwned: []string{},
			MayCommit: e.effectiveActorCommitPermission(agent),
		},
		Executor: provider.ExecutorCapabilities{
			Sandbox: agent.Sandbox, Approval: agent.Approval, Ephemeral: agent.Ephemeral,
			FilesystemBoundary: true,
		},
		Manifest:    invocationContextManifest(role == invocationRoleRepair),
		Validations: []provider.ValidationRequirement{},
	}
	if phase != nil {
		context.Invocation.Phase = phase.ID
		context.Invocation.PhaseKind = phase.Kind
		context.Authority.ReadOnly = phase.ReadOnly
		if phase.Kind == "criterion" {
			if _, criterion, err := e.phaseCriterion(phase); err == nil {
				context.Invocation.Criterion = criterion
			}
		}
	}
	if role == invocationRoleRepair && len(validations) == 1 {
		context.Invocation.Validation = validations[0]
	}

	head, err := e.Repo.Head()
	if err != nil {
		return provider.InvocationContext{}, fmt.Errorf("compile invocation workspace HEAD: %w", err)
	}
	changed, err := e.changedImplementationFiles()
	if err != nil {
		return provider.InvocationContext{}, fmt.Errorf("compile invocation changed paths: %w", err)
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return provider.InvocationContext{}, fmt.Errorf("compile invocation dirty paths: %w", err)
	}
	sort.Strings(changed)
	sort.Strings(dirty)
	context.Workspace.Head = head
	context.Workspace.ChangedPaths = nonNilStrings(changed)
	context.Workspace.DirtyPaths = nonNilStrings(dirty)

	dependencies, err := e.compileDependencyContext(phase)
	if err != nil {
		return provider.InvocationContext{}, err
	}
	context.Dependencies = dependencies
	artifacts, evidence, err := e.compileContractContext(phase)
	if err != nil {
		return provider.InvocationContext{}, err
	}
	context.Artifacts = artifacts
	context.Evidence = evidence

	x := e.context(phase)
	if !context.Authority.ReadOnly {
		writes, err := e.effectivePhaseWrites(phase)
		if err != nil {
			return provider.InvocationContext{}, err
		}
		for _, path := range writes {
			expanded, err := expandContextPath(x, path, e.Repo.Root)
			if err != nil {
				return provider.InvocationContext{}, fmt.Errorf("compile writable path %q: %w", path, err)
			}
			context.Authority.WritablePaths = append(context.Authority.WritablePaths, expanded)
		}
	}
	for _, rule := range e.Workflow.Spec.Workspace.MutationPolicy.Integrity {
		for _, path := range rule.Paths {
			if gitstate.IsActorPrivatePath(path) {
				continue
			}
			expandedPath, err := expandContextPath(x, path, e.Repo.Root)
			if err != nil {
				return provider.InvocationContext{}, fmt.Errorf("compile protected path %q: %w", path, err)
			}
			excludes := make([]string, 0, len(rule.Exclude))
			for _, exclude := range rule.Exclude {
				expanded, err := expandContextPath(x, exclude, e.Repo.Root)
				if err != nil {
					return provider.InvocationContext{}, fmt.Errorf("compile protected exclusion %q: %w", exclude, err)
				}
				excludes = append(excludes, expanded)
			}
			context.Authority.Protected = append(context.Authority.Protected, provider.ProtectedPath{
				Rule: rule.ID, Path: expandedPath, Excludes: nonNilStrings(excludes), Mode: rule.Mode,
			})
		}
	}
	runtimeOwned, err := e.engineOwnedProgressFiles(x, phase)
	if err != nil {
		return provider.InvocationContext{}, err
	}
	for i := range runtimeOwned {
		runtimeOwned[i] = remapWorkspacePathReferences(runtimeOwned[i], e.Repo.Root, provider.WorkspacePlaceholder)
	}
	context.Authority.RuntimeOwned = nonNilStrings(runtimeOwned)
	excluded := make([]string, 0, len(context.Authority.Protected)+len(runtimeOwned))
	for _, protected := range context.Authority.Protected {
		excluded = append(excluded, protected.Path)
	}
	excluded = append(excluded, runtimeOwned...)
	context.Authority.Resources = provider.ResourceAccess{
		WorkspaceRead:  "full-quarantine-workspace",
		WorkspaceWrite: nonNilStrings(append([]string(nil), context.Authority.WritablePaths...)),
		ExcludedPaths:  nonNilStrings(excluded),
	}
	for _, validation := range validations {
		context.Validations = append(context.Validations, provider.ValidationRequirement{Name: validation})
	}
	if role == invocationRoleRepair {
		failure, err := e.compileRepairFailure(phase, context.Invocation.Validation)
		if err != nil {
			return provider.InvocationContext{}, err
		}
		context.Failure = failure
	}
	return context, nil
}

func expandContextPath(context workflow.Context, path, root string) (string, error) {
	expanded, err := context.Expand(path)
	if err != nil {
		return "", err
	}
	return remapWorkspacePathReferences(expanded, root, provider.WorkspacePlaceholder), nil
}

func (e *Engine) compileDependencyContext(phase *workflow.Phase) ([]provider.DependencyContext, error) {
	dependencies := []provider.DependencyContext{}
	if phase == nil {
		return dependencies, nil
	}
	for _, phaseID := range e.Workflow.DependencyGraph.Dependencies(phase.ID) {
		dependency, err := e.phaseByID(phaseID)
		if err != nil {
			return nil, err
		}
		commit, ok, err := e.Store.Resolve(e.phaseMarkerName(dependency))
		if err != nil {
			return nil, err
		}
		if !ok || !e.Repo.ObjectExists(commit+"^{commit}") || !e.Repo.IsAncestor(commit, "HEAD") {
			return nil, fmt.Errorf("phase %s dependency %s has no accepted commit identity", phase.ID, phaseID)
		}
		dependencies = append(dependencies, provider.DependencyContext{Phase: phaseID, Commit: commit})
	}
	return dependencies, nil
}

func (e *Engine) compileContractContext(phase *workflow.Phase) ([]provider.ArtifactReference, []provider.EvidenceReference, error) {
	artifacts := []provider.ArtifactReference{}
	evidence := []provider.EvidenceReference{}
	if phase == nil {
		return artifacts, evidence, nil
	}
	for _, input := range phase.Inputs {
		if input.Artifact != "" {
			record, err := e.loadContractArtifactInput(phase, input.Artifact)
			if err != nil {
				return nil, nil, err
			}
			for _, file := range record.Files {
				artifacts = append(artifacts, provider.ArtifactReference{
					Name: record.Name, Producer: record.Producer, Type: record.Type,
					Path: provider.WorkspacePlaceholder + "/" + filepath.ToSlash(file.Path), Digest: file.Digest, Mode: file.Mode,
				})
			}
		}
		if input.Evidence != "" {
			reference, err := e.compileEvidenceReference(phase, input.Evidence)
			if err != nil {
				return nil, nil, err
			}
			evidence = append(evidence, reference)
		}
	}
	return artifacts, evidence, nil
}

func (e *Engine) compileEvidenceReference(consumer *workflow.Phase, name string) (provider.EvidenceReference, error) {
	if _, ok := e.Workflow.Spec.Contracts.Evidence[name]; !ok {
		return provider.EvidenceReference{}, fmt.Errorf("phase %s requires undeclared evidence %q", consumer.ID, name)
	}
	for _, dependency := range e.Workflow.DependencyGraph.Dependencies(consumer.ID) {
		phase, err := e.phaseByID(dependency)
		if err != nil {
			return provider.EvidenceReference{}, err
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
				return provider.EvidenceReference{}, err
			}
			if !ok || record.Version != contractRecordVersion || record.Name != name || record.Producer != phase.ID || record.Validation != validationName {
				return provider.EvidenceReference{}, fmt.Errorf("phase %s requires compatible evidence %q from phase %s", consumer.ID, name, phase.ID)
			}
			return provider.EvidenceReference{Name: name, Producer: record.Producer, Validation: record.Validation}, nil
		}
	}
	return provider.EvidenceReference{}, fmt.Errorf("phase %s has no dependency that produces evidence %q", consumer.ID, name)
}

func (e *Engine) compileRepairFailure(phase *workflow.Phase, validation string) (*provider.RepairFailureEvidence, error) {
	if validation == "" {
		return nil, fmt.Errorf("repair invocation has no selected validation")
	}
	if phase != nil {
		var active ActivePhase
		ok, err := e.Store.GetJSON(e.activeRecord(), &active)
		if err != nil {
			return nil, err
		}
		if !ok || active.PhaseID != phase.ID || active.Validation != validation || active.FailureKind != PhaseFailureValidation || active.ValidationError == "" {
			return nil, fmt.Errorf("repair invocation for validation %s has no compatible bounded failure evidence", validation)
		}
		return &provider.RepairFailureEvidence{Validation: validation, Kind: string(active.FailureKind), Output: active.ValidationError}, nil
	}
	var failure validationFailureEvidence
	ok, err := e.Store.GetJSON(e.standaloneFailureRecord(validation), &failure)
	if err != nil {
		return nil, err
	}
	if !ok || failure.Validation != validation || failure.FailureKind != PhaseFailureValidation || failure.Output == "" {
		return nil, fmt.Errorf("repair invocation for validation %s has no compatible bounded failure evidence", validation)
	}
	return &provider.RepairFailureEvidence{Validation: validation, Kind: string(failure.FailureKind), Output: failure.Output}, nil
}

func invocationContextManifest(includeFailure bool) provider.ContextManifest {
	included := []provider.ContextManifestEntry{
		{Component: "invocation identity", Source: "normalized phase and selected actor authority", Reason: "identify the bounded unit of work"},
		{Component: "expanded objective", Source: "authored phase or repair objective", Reason: "state the actor-owned outcome"},
		{Component: "workspace state", Source: "Git workspace projection", Reason: "inspect relevant retained and current work", RuntimeResolved: true},
		{Component: "direct dependency identities", Source: "durable accepted phase markers", Reason: "bind inputs to accepted producers", RuntimeResolved: true},
		{Component: "declared artifact references", Source: "durable typed contract records and verified workspace files", Reason: "provide only declared typed handoffs", RuntimeResolved: true},
		{Component: "declared deterministic evidence", Source: "durable typed evidence records", Reason: "provide only declared validation claims", RuntimeResolved: true},
		{Component: "effective authority", Source: "normalized workspace, phase, and actor policy", Reason: "describe runtime-enforced mutation and commit boundaries"},
		{Component: "executor capabilities", Source: "normalized actor and provider boundary", Reason: "describe effective execution capabilities"},
		{Component: "required validations", Source: "normalized lifecycle and selected validation policy", Reason: "identify deterministic checks required for acceptance"},
	}
	if includeFailure {
		included = append(included, provider.ContextManifestEntry{Component: "selected repair failure", Source: "durable bounded and redacted validation failure", Reason: "focus repair on the failed deterministic validation", RuntimeResolved: true})
	}
	excluded := []provider.ContextManifestEntry{
		{Component: "timestamps and random quarantine paths", Source: "runtime implementation detail", Reason: "non-deterministic and unnecessary"},
		{Component: "transcripts and provider output", Source: "prior provider execution", Reason: "not workflow authority"},
		{Component: "unrelated contracts and broad run history", Source: "durable runtime state", Reason: "not declared as direct invocation input"},
		{Component: "artifact contents", Source: "workspace files", Reason: "actors read verified references from the isolated workspace"},
		{Component: "resolved parameters, environments, and secrets", Source: "runtime inputs", Reason: "only their authorized expansions may appear in the objective"},
		{Component: "token and resource budgets", Source: "deferred roadmap work", Reason: "enforcement is explicitly deferred"},
	}
	return provider.ContextManifest{Included: included, Excluded: excluded}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
