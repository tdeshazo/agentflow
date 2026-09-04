package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

const invocationContextCeiling = 65536

// contextUnit is the internal, deterministic selection IR. It deliberately
// keeps provenance and policy separate from provider-visible payloads.
type contextUnit struct {
	ID               string
	Required         bool
	Scope            string
	Provenance       string
	ProvenanceDigest string
	Preserve         string
	Value            semanticContextValue
}

type dependencyContextUnit struct {
	Dependency semanticDependencyContext
	Artifacts  []semanticArtifactReference
	Evidence   []semanticEvidenceReference
	Handoffs   []semanticHandoffReference
}

// semanticContextValue makes every selectable value explicit. Provider wire
// values cannot enter the semantic compiler through an untyped container.
type semanticContextValue interface{ semanticContextValue() }

func (semanticInvocationIdentity) semanticContextValue()    {}
func (semanticWorkspaceContext) semanticContextValue()      {}
func (semanticInvocationAuthority) semanticContextValue()   {}
func (semanticExecutorCapabilities) semanticContextValue()  {}
func (semanticValidationRequirement) semanticContextValue() {}
func (semanticDependencyContext) semanticContextValue()     {}
func (semanticArtifactReference) semanticContextValue()     {}
func (semanticEvidenceReference) semanticContextValue()     {}
func (semanticHandoffReference) semanticContextValue()      {}
func (dependencyContextUnit) semanticContextValue()         {}
func (semanticObjective) semanticContextValue()             {}

type semanticObjective string

const (
	invocationRolePhase       = "phase"
	invocationRolePhaseResume = "phase-resume"
	invocationRoleRepair      = "validation-repair"
)

// compileInvocationContext derives a provider-independent semantic result
// from normalized workflow authority and current durable/workspace state.
// It is intentionally not persisted.
func (e *Engine) compileInvocationContext(actorName, role, objective string, agent workflow.Agent, phase *workflow.Phase, validations []string) (semanticInvocationContext, error) {
	policy, err := e.effectiveExecutionPolicy(agent)
	if err != nil {
		return semanticInvocationContext{}, err
	}
	credentialNames := make([]string, 0, len(policy.Credentials))
	for _, credential := range policy.Credentials {
		credentialNames = append(credentialNames, credential.Name)
	}
	sort.Strings(credentialNames)
	context := semanticInvocationContext{
		Encoding: semanticContextEncoding,
		Invocation: semanticInvocationIdentity{
			Role:  role,
			Actor: actorName,
		},
		Objective:    remapWorkspacePathReferences(objective, e.Repo.Root, semanticWorkspacePlaceholder),
		Workspace:    semanticWorkspaceContext{Root: semanticWorkspacePlaceholder},
		Dependencies: []semanticDependencyContext{},
		Artifacts:    []semanticArtifactReference{},
		Evidence:     []semanticEvidenceReference{},
		Handoffs:     []semanticHandoffReference{},
		Authority: semanticInvocationAuthority{
			WritablePaths: []string{}, Protected: []semanticProtectedPath{}, RuntimeOwned: []string{},
			MayCommit: e.effectiveActorCommitPermission(agent),
		},
		Executor: semanticExecutorCapabilities{
			Sandbox: agent.Sandbox, Approval: agent.Approval, Ephemeral: agent.Ephemeral,
			FilesystemBoundary: true, Network: policy.Network,
			Capabilities: append([]string(nil), policy.Capabilities...), Credentials: credentialNames,
			ApprovalGate: policy.ApprovalGate,
			Budgets: semanticResourceBudgets{
				ModelCalls: policy.Budgets.ModelCalls, ToolCalls: policy.Budgets.ToolCalls,
				Tokens: policy.Budgets.Tokens, Duration: policy.Budgets.Duration, CostUSD: policy.Budgets.CostUSD,
			},
		},
		Manifest:    invocationContextManifest(role == invocationRoleRepair),
		Validations: []semanticValidationRequirement{},
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
		return semanticInvocationContext{}, fmt.Errorf("compile invocation workspace HEAD: %w", err)
	}
	changed, err := e.changedImplementationFiles()
	if err != nil {
		return semanticInvocationContext{}, fmt.Errorf("compile invocation changed paths: %w", err)
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return semanticInvocationContext{}, fmt.Errorf("compile invocation dirty paths: %w", err)
	}
	sort.Strings(changed)
	sort.Strings(dirty)
	context.Workspace.Head = head
	context.Workspace.ChangedPaths = nonNilStrings(changed)
	context.Workspace.DirtyPaths = nonNilStrings(dirty)

	dependencies, err := e.compileDependencyContext(phase)
	if err != nil {
		return semanticInvocationContext{}, err
	}
	context.Dependencies = dependencies
	artifacts, evidence, err := e.compileContractContext(phase)
	if err != nil {
		return semanticInvocationContext{}, err
	}
	context.Artifacts = artifacts
	context.Evidence = evidence
	x := e.context(phase)
	if !context.Authority.ReadOnly {
		writes, err := e.effectivePhaseWrites(phase)
		if err != nil {
			return semanticInvocationContext{}, err
		}
		for _, path := range writes {
			expanded, err := expandContextPath(x, path, e.Repo.Root)
			if err != nil {
				return semanticInvocationContext{}, fmt.Errorf("compile writable path %q: %w", path, err)
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
				return semanticInvocationContext{}, fmt.Errorf("compile protected path %q: %w", path, err)
			}
			excludes := make([]string, 0, len(rule.Exclude))
			for _, exclude := range rule.Exclude {
				expanded, err := expandContextPath(x, exclude, e.Repo.Root)
				if err != nil {
					return semanticInvocationContext{}, fmt.Errorf("compile protected exclusion %q: %w", exclude, err)
				}
				excludes = append(excludes, expanded)
			}
			context.Authority.Protected = append(context.Authority.Protected, semanticProtectedPath{
				Rule: rule.ID, Path: expandedPath, Excludes: nonNilStrings(excludes), Mode: rule.Mode,
			})
		}
	}
	runtimeOwned, err := e.engineOwnedProgressFiles(x, phase)
	if err != nil {
		return semanticInvocationContext{}, err
	}
	for i := range runtimeOwned {
		runtimeOwned[i] = remapWorkspacePathReferences(runtimeOwned[i], e.Repo.Root, semanticWorkspacePlaceholder)
	}
	context.Authority.RuntimeOwned = nonNilStrings(runtimeOwned)
	excluded := make([]string, 0, len(context.Authority.Protected)+len(runtimeOwned))
	for _, protected := range context.Authority.Protected {
		excluded = append(excluded, protected.Path)
	}
	excluded = append(excluded, runtimeOwned...)
	context.Authority.Resources = semanticResourceAccess{
		WorkspaceRead:  "full-quarantine-workspace",
		WorkspaceWrite: nonNilStrings(append([]string(nil), context.Authority.WritablePaths...)),
		ExcludedPaths:  nonNilStrings(excluded),
	}
	for _, validation := range validations {
		context.Validations = append(context.Validations, semanticValidationRequirement{Name: validation})
	}
	if role == invocationRoleRepair {
		failure, err := e.compileRepairFailure(phase, context.Invocation.Validation)
		if err != nil {
			return semanticInvocationContext{}, err
		}
		context.Failure = failure
	}
	return context, nil
}

// compileFreshInvocationContext deterministically selects a semantic context.
// Provider negotiation and wire-version selection happen only during
// projection after this function returns.
func (e *Engine) compileFreshInvocationContext(context semanticInvocationContext) (semanticInvocationContext, error) {
	context.Fresh = true
	units := []contextUnit{
		{ID: "identity", Required: true, Scope: "invocation", Provenance: "normalized workflow", Preserve: "required", Value: context.Invocation},
		{ID: "objective", Required: true, Scope: "invocation", Provenance: "expanded phase objective", Preserve: "required", Value: semanticObjective(context.Objective)},
		{ID: "workspace", Required: true, Scope: "workspace", Provenance: "git projection", Preserve: "required", Value: context.Workspace},
		{ID: "authority", Required: true, Scope: "invocation", Provenance: "effective execution policy", Preserve: "required", Value: context.Authority},
		{ID: "executor", Required: true, Scope: "invocation", Provenance: "effective actor policy", Preserve: "required", Value: context.Executor},
		{ID: "validations", Required: true, Scope: "phase", Provenance: "normalized lifecycle", Preserve: "required", Value: semanticValidationValues(context.Validations)},
	}
	usedArtifacts, usedEvidence, usedHandoffs := make([]bool, len(context.Artifacts)), make([]bool, len(context.Evidence)), make([]bool, len(context.Handoffs))
	for i, dependency := range context.Dependencies {
		group := dependencyContextUnit{Dependency: dependency, Artifacts: []semanticArtifactReference{}, Evidence: []semanticEvidenceReference{}, Handoffs: []semanticHandoffReference{}}
		for index, artifact := range context.Artifacts {
			if artifact.Producer == dependency.Phase {
				group.Artifacts = append(group.Artifacts, artifact)
				usedArtifacts[index] = true
			}
		}
		for index, evidence := range context.Evidence {
			if evidence.Producer == dependency.Phase {
				group.Evidence = append(group.Evidence, evidence)
				usedEvidence[index] = true
			}
		}
		for index, handoff := range context.Handoffs {
			if handoff.Producer == dependency.Phase {
				group.Handoffs = append(group.Handoffs, handoff)
				usedHandoffs[index] = true
			}
		}
		units = append(units, contextUnit{ID: fmt.Sprintf("dependency/%03d", i), Scope: "direct-dependency", Provenance: "accepted phase marker and typed direct-dependency inputs", Preserve: "dependency-closure", Value: group})
	}
	for i, value := range context.Artifacts {
		if !usedArtifacts[i] {
			units = append(units, contextUnit{ID: fmt.Sprintf("artifact/%03d", i), Scope: "direct-dependency", Provenance: "verified contract record", Preserve: "atomic", Value: value})
		}
	}
	for i, value := range context.Evidence {
		if !usedEvidence[i] {
			units = append(units, contextUnit{ID: fmt.Sprintf("evidence/%03d", i), Scope: "direct-dependency", Provenance: "deterministic validation", Preserve: "atomic", Value: value})
		}
	}
	for i, value := range context.Handoffs {
		if !usedHandoffs[i] {
			units = append(units, contextUnit{ID: fmt.Sprintf("handoff/%03d", i), Scope: "direct-dependency", Provenance: "accepted advisory handoff", Preserve: "atomic", Value: value})
		}
	}

	selected := make([]string, 0, len(units))
	selectedUnits := make([]contextUnit, 0, len(units))
	omitted := []semanticContextOmission{}
	// Dependencies are a closure: they and their typed derivative units are
	// selected in sorted unit order or omitted as a group when space is tight.
	context.Dependencies, context.Artifacts, context.Evidence, context.Handoffs = []semanticDependencyContext{}, []semanticArtifactReference{}, []semanticEvidenceReference{}, []semanticHandoffReference{}
	context.Receipt = nil
	base := context
	for _, unit := range units {
		if unit.ProvenanceDigest == "" {
			digest := sha256.Sum256([]byte(unit.Provenance))
			unit.ProvenanceDigest = "sha256:" + hex.EncodeToString(digest[:])
		}
		candidate := applyContextUnit(context, unit)
		candidateSelected := append(append([]string(nil), selected...), unit.ID)
		candidate, bytes, err := finalizeFreshContext(candidate, candidateSelected, omitted)
		if err != nil {
			return semanticInvocationContext{}, err
		}
		if len(bytes) > invocationContextCeiling {
			if unit.Required {
				return semanticInvocationContext{}, fmt.Errorf("mandatory fresh context unit %q cannot fit fixed %d-byte ceiling", unit.ID, invocationContextCeiling)
			}
			omitted = append(omitted, semanticContextOmission{ID: unit.ID, Reason: "fixed-ceiling"})
			continue
		}
		context = candidate
		selected = candidateSelected
		selectedUnits = append(selectedUnits, unit)
	}
	context, bytes, err := finalizeFreshContext(context, selected, omitted)
	if err != nil {
		return semanticInvocationContext{}, err
	}
	for len(bytes) > invocationContextCeiling {
		remove := -1
		for index := len(selectedUnits) - 1; index >= 0; index-- {
			if !selectedUnits[index].Required {
				remove = index
				break
			}
		}
		if remove < 0 {
			return semanticInvocationContext{}, fmt.Errorf("mandatory fresh context cannot fit fixed %d-byte ceiling", invocationContextCeiling)
		}
		omitted = append(omitted, semanticContextOmission{ID: selectedUnits[remove].ID, Reason: "fixed-ceiling"})
		selectedUnits = append(selectedUnits[:remove], selectedUnits[remove+1:]...)
		selected = append(selected[:remove], selected[remove+1:]...)
		context = base
		for _, unit := range selectedUnits {
			context = applyContextUnit(context, unit)
		}
		context, bytes, err = finalizeFreshContext(context, selected, omitted)
		if err != nil {
			return semanticInvocationContext{}, err
		}
	}
	return context, nil
}

type semanticValidationValues []semanticValidationRequirement

func (semanticValidationValues) semanticContextValue() {}

func applyContextUnit(context semanticInvocationContext, unit contextUnit) semanticInvocationContext {
	switch value := unit.Value.(type) {
	case semanticInvocationIdentity:
		context.Invocation = value
	case semanticObjective:
		context.Objective = string(value)
	case semanticWorkspaceContext:
		context.Workspace = value
	case semanticInvocationAuthority:
		context.Authority = value
	case semanticExecutorCapabilities:
		context.Executor = value
	case semanticValidationValues:
		context.Validations = []semanticValidationRequirement(value)
	case dependencyContextUnit:
		context.Dependencies = append(context.Dependencies, value.Dependency)
		context.Artifacts = append(context.Artifacts, value.Artifacts...)
		context.Evidence = append(context.Evidence, value.Evidence...)
		context.Handoffs = append(context.Handoffs, value.Handoffs...)
	case semanticDependencyContext:
		context.Dependencies = append(context.Dependencies, value)
	case semanticArtifactReference:
		context.Artifacts = append(context.Artifacts, value)
	case semanticEvidenceReference:
		context.Evidence = append(context.Evidence, value)
	case semanticHandoffReference:
		context.Handoffs = append(context.Handoffs, value)
	}
	return context
}

func finalizeFreshContext(context semanticInvocationContext, selected []string, omitted []semanticContextOmission) (semanticInvocationContext, []byte, error) {
	context.Receipt = &semanticContextReceipt{CompilerVersion: "v2", Selected: append([]string(nil), selected...), Omitted: append([]semanticContextOmission(nil), omitted...)}
	unsigned, err := canonicalContextBytes(context)
	if err != nil {
		return semanticInvocationContext{}, nil, err
	}
	digest := sha256.Sum256(unsigned)
	context.Receipt.Digest = "sha256:" + hex.EncodeToString(digest[:])
	for {
		encoded, err := canonicalContextBytes(context)
		if err != nil {
			return semanticInvocationContext{}, nil, err
		}
		if context.Receipt.Bytes == len(encoded) {
			return context, encoded, nil
		}
		context.Receipt.Bytes = len(encoded)
	}
}

func canonicalContextBytes(context semanticInvocationContext) ([]byte, error) {
	return json.Marshal(context)
}

func expandContextPath(context workflow.Context, path, root string) (string, error) {
	expanded, err := context.Expand(path)
	if err != nil {
		return "", err
	}
	return remapWorkspacePathReferences(expanded, root, semanticWorkspacePlaceholder), nil
}

func (e *Engine) compileDependencyContext(phase *workflow.Phase) ([]semanticDependencyContext, error) {
	dependencies := []semanticDependencyContext{}
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
		dependencies = append(dependencies, semanticDependencyContext{Phase: phaseID, Commit: commit})
	}
	return dependencies, nil
}

func (e *Engine) compileContractContext(phase *workflow.Phase) ([]semanticArtifactReference, []semanticEvidenceReference, error) {
	artifacts := []semanticArtifactReference{}
	evidence := []semanticEvidenceReference{}
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
				artifacts = append(artifacts, semanticArtifactReference{
					Name: record.Name, Producer: record.Producer, Type: record.Type,
					Path: semanticWorkspacePlaceholder + "/" + filepath.ToSlash(file.Path), Digest: file.Digest, Mode: file.Mode,
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

func (e *Engine) compileEvidenceReference(consumer *workflow.Phase, name string) (semanticEvidenceReference, error) {
	if _, ok := e.Workflow.Spec.Contracts.Evidence[name]; !ok {
		return semanticEvidenceReference{}, fmt.Errorf("phase %s requires undeclared evidence %q", consumer.ID, name)
	}
	for _, dependency := range e.Workflow.DependencyGraph.Dependencies(consumer.ID) {
		phase, err := e.phaseByID(dependency)
		if err != nil {
			return semanticEvidenceReference{}, err
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
				return semanticEvidenceReference{}, err
			}
			if !ok || record.Version != contractRecordVersion || record.Name != name || record.Producer != phase.ID || record.Validation != validationName {
				return semanticEvidenceReference{}, fmt.Errorf("phase %s requires compatible evidence %q from phase %s", consumer.ID, name, phase.ID)
			}
			return semanticEvidenceReference{Name: name, Producer: record.Producer, Validation: record.Validation}, nil
		}
	}
	return semanticEvidenceReference{}, fmt.Errorf("phase %s has no dependency that produces evidence %q", consumer.ID, name)
}

func (e *Engine) compileRepairFailure(phase *workflow.Phase, validation string) (*semanticRepairFailureEvidence, error) {
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
		return &semanticRepairFailureEvidence{Validation: validation, Kind: string(active.FailureKind), Output: active.ValidationError}, nil
	}
	var failure validationFailureEvidence
	ok, err := e.Store.GetJSON(e.standaloneFailureRecord(validation), &failure)
	if err != nil {
		return nil, err
	}
	if !ok || failure.Validation != validation || failure.FailureKind != PhaseFailureValidation || failure.Output == "" {
		return nil, fmt.Errorf("repair invocation for validation %s has no compatible bounded failure evidence", validation)
	}
	return &semanticRepairFailureEvidence{Validation: validation, Kind: string(failure.FailureKind), Output: failure.Output}, nil
}

func invocationContextManifest(includeFailure bool) semanticContextManifest {
	included := []semanticContextManifestEntry{
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
		included = append(included, semanticContextManifestEntry{Component: "selected repair failure", Source: "durable bounded and redacted validation failure", Reason: "focus repair on the failed deterministic validation", RuntimeResolved: true})
	}
	excluded := []semanticContextManifestEntry{
		{Component: "timestamps and random quarantine paths", Source: "runtime implementation detail", Reason: "non-deterministic and unnecessary"},
		{Component: "transcripts and provider output", Source: "prior provider execution", Reason: "not workflow authority"},
		{Component: "unrelated contracts and broad run history", Source: "durable runtime state", Reason: "not declared as direct invocation input"},
		{Component: "artifact contents", Source: "workspace files", Reason: "actors read verified references from the isolated workspace"},
		{Component: "resolved parameters, environments, and secrets", Source: "runtime inputs", Reason: "only their authorized expansions may appear in the objective"},
		{Component: "token and resource budgets", Source: "deferred roadmap work", Reason: "enforcement is explicitly deferred"},
	}
	return semanticContextManifest{Included: included, Excluded: excluded}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
