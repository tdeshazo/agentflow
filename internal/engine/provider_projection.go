package engine

import "github.com/tdeshazo/agentflow/provider"

// projectInvocationContext is the only compiler-adjacent provider boundary.
// Selection, omission, digest, and byte accounting have completed before this
// projection chooses the negotiated provider representation.
func projectInvocationContext(context semanticInvocationContext) provider.InvocationContext {
	version := provider.InvocationContextVersionV1
	if context.Fresh {
		version = provider.InvocationContextVersionV2
	}
	projected := provider.InvocationContext{
		Version: version,
		Invocation: provider.InvocationIdentity{
			Role: context.Invocation.Role, Actor: context.Invocation.Actor, Phase: context.Invocation.Phase,
			PhaseKind: context.Invocation.PhaseKind, Criterion: context.Invocation.Criterion, Validation: context.Invocation.Validation,
		},
		Objective:    context.Objective,
		Workspace:    provider.WorkspaceContext{Root: context.Workspace.Root, Head: context.Workspace.Head, ChangedPaths: append([]string(nil), context.Workspace.ChangedPaths...), DirtyPaths: append([]string(nil), context.Workspace.DirtyPaths...)},
		Dependencies: projectDependencies(context.Dependencies),
		Artifacts:    projectArtifacts(context.Artifacts),
		Evidence:     projectEvidence(context.Evidence),
		Handoffs:     projectHandoffReferences(context.Handoffs),
		Authority:    projectAuthority(context.Authority),
		Executor:     projectExecutor(context.Executor),
		Validations:  projectValidations(context.Validations),
		Manifest:     projectManifest(context.Manifest),
	}
	if context.Failure != nil {
		projected.Failure = &provider.RepairFailureEvidence{Validation: context.Failure.Validation, Kind: context.Failure.Kind, Output: context.Failure.Output}
	}
	if context.Receipt != nil {
		projected.Receipt = &provider.ContextReceipt{CompilerVersion: context.Receipt.CompilerVersion, Digest: context.Receipt.Digest, Bytes: context.Receipt.Bytes, Selected: append([]string(nil), context.Receipt.Selected...), Omitted: projectOmissions(context.Receipt.Omitted)}
	}
	return projected
}

func semanticHandoffFromProvider(h provider.Handoff) semanticHandoff {
	changes := make([]semanticHandoffChange, 0, len(h.Changes))
	for _, change := range h.Changes {
		changes = append(changes, semanticHandoffChange{Path: change.Path, Summary: change.Summary})
	}
	findings := make([]semanticHandoffFinding, 0, len(h.Findings))
	for _, finding := range h.Findings {
		findings = append(findings, semanticHandoffFinding{Severity: finding.Severity, Summary: finding.Summary})
	}
	return semanticHandoff{Encoding: semanticHandoffEncoding, Status: h.Status, Summary: h.Summary, Changes: changes, Findings: findings, Checks: append([]string(nil), h.Checks...), Risks: append([]string(nil), h.Risks...), Blockers: append([]string(nil), h.Blockers...), NextActions: append([]string(nil), h.NextActions...)}
}

func projectHandoff(h semanticHandoff) provider.Handoff {
	changes := make([]provider.HandoffChange, 0, len(h.Changes))
	for _, change := range h.Changes {
		changes = append(changes, provider.HandoffChange{Path: change.Path, Summary: change.Summary})
	}
	findings := make([]provider.HandoffFinding, 0, len(h.Findings))
	for _, finding := range h.Findings {
		findings = append(findings, provider.HandoffFinding{Severity: finding.Severity, Summary: finding.Summary})
	}
	return provider.Handoff{Version: provider.HandoffVersionV1, Status: h.Status, Summary: h.Summary, Changes: changes, Findings: findings, Checks: append([]string(nil), h.Checks...), Risks: append([]string(nil), h.Risks...), Blockers: append([]string(nil), h.Blockers...), NextActions: append([]string(nil), h.NextActions...)}
}

func projectDependencies(values []semanticDependencyContext) []provider.DependencyContext {
	result := make([]provider.DependencyContext, 0, len(values))
	for _, value := range values {
		result = append(result, provider.DependencyContext{Phase: value.Phase, Commit: value.Commit})
	}
	return result
}
func projectArtifacts(values []semanticArtifactReference) []provider.ArtifactReference {
	result := make([]provider.ArtifactReference, 0, len(values))
	for _, value := range values {
		result = append(result, provider.ArtifactReference{Name: value.Name, Producer: value.Producer, Type: value.Type, Path: value.Path, Digest: value.Digest, Mode: value.Mode})
	}
	return result
}
func projectEvidence(values []semanticEvidenceReference) []provider.EvidenceReference {
	result := make([]provider.EvidenceReference, 0, len(values))
	for _, value := range values {
		result = append(result, provider.EvidenceReference{Name: value.Name, Producer: value.Producer, Validation: value.Validation})
	}
	return result
}
func projectHandoffReferences(values []semanticHandoffReference) []provider.HandoffReference {
	result := make([]provider.HandoffReference, 0, len(values))
	for _, value := range values {
		result = append(result, provider.HandoffReference{Producer: value.Producer, Commit: value.Commit, Digest: value.Digest, Payload: projectHandoff(value.Payload)})
	}
	return result
}
func projectAuthority(value semanticInvocationAuthority) provider.InvocationAuthority {
	protected := make([]provider.ProtectedPath, 0, len(value.Protected))
	for _, path := range value.Protected {
		protected = append(protected, provider.ProtectedPath{Rule: path.Rule, Path: path.Path, Excludes: append([]string(nil), path.Excludes...), Mode: path.Mode})
	}
	return provider.InvocationAuthority{WritablePaths: append([]string(nil), value.WritablePaths...), Protected: protected, ReadOnly: value.ReadOnly, RuntimeOwned: append([]string(nil), value.RuntimeOwned...), MayCommit: value.MayCommit, Resources: provider.ResourceAccess{WorkspaceRead: value.Resources.WorkspaceRead, WorkspaceWrite: append([]string(nil), value.Resources.WorkspaceWrite...), ExcludedPaths: append([]string(nil), value.Resources.ExcludedPaths...)}}
}
func projectExecutor(value semanticExecutorCapabilities) provider.ExecutorCapabilities {
	return provider.ExecutorCapabilities{Sandbox: value.Sandbox, Approval: value.Approval, Ephemeral: value.Ephemeral, FilesystemBoundary: value.FilesystemBoundary, Network: value.Network, Capabilities: append([]string(nil), value.Capabilities...), Credentials: append([]string(nil), value.Credentials...), ApprovalGate: value.ApprovalGate, Budgets: provider.ResourceBudgets{ModelCalls: value.Budgets.ModelCalls, ToolCalls: value.Budgets.ToolCalls, Tokens: value.Budgets.Tokens, Duration: value.Budgets.Duration, CostUSD: value.Budgets.CostUSD}}
}
func projectValidations(values []semanticValidationRequirement) []provider.ValidationRequirement {
	result := make([]provider.ValidationRequirement, 0, len(values))
	for _, value := range values {
		result = append(result, provider.ValidationRequirement{Name: value.Name})
	}
	return result
}
func projectManifest(value semanticContextManifest) provider.ContextManifest {
	return provider.ContextManifest{Included: projectManifestEntries(value.Included), Excluded: projectManifestEntries(value.Excluded)}
}
func projectManifestEntries(values []semanticContextManifestEntry) []provider.ContextManifestEntry {
	result := make([]provider.ContextManifestEntry, 0, len(values))
	for _, value := range values {
		result = append(result, provider.ContextManifestEntry{Component: value.Component, Source: value.Source, Reason: value.Reason, RuntimeResolved: value.RuntimeResolved})
	}
	return result
}
func projectOmissions(values []semanticContextOmission) []provider.ContextOmission {
	result := make([]provider.ContextOmission, 0, len(values))
	for _, value := range values {
		result = append(result, provider.ContextOmission{ID: value.ID, Reason: value.Reason})
	}
	return result
}
