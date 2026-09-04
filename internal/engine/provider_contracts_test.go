package engine

import "github.com/tdeshazo/agentflow/provider"

func testActorContract() provider.Contract {
	return provider.Contract{
		Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersion}, FilesystemBoundary: true, ExecutionPolicy: true,
	}
}

func structuredTestActorContract() provider.Contract {
	return provider.Contract{
		Version: provider.ContractVersionV2, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV1, provider.InvocationContextVersionV2},
		FilesystemBoundary:        true, ExecutionPolicy: true, HandoffVersions: []string{provider.HandoffVersionV1},
	}
}

func structuredTestResult(request provider.Request) provider.Result {
	if request.Handoff == nil {
		return provider.Result{}
	}
	return provider.Result{Handoff: []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"complete","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`)}
}

func (p *schedulingProvider) Contract() provider.Contract {
	if p.structured {
		return structuredTestActorContract()
	}
	return testActorContract()
}
func (*checklistProvider) Contract() provider.Contract         { return testActorContract() }
func (*engineOwnedProvider) Contract() provider.Contract       { return testActorContract() }
func (*referenceWorkflowProvider) Contract() provider.Contract { return structuredTestActorContract() }
func (*canonicalSelfHostingProvider) Contract() provider.Contract {
	return structuredTestActorContract()
}
func (*writeProvider) Contract() provider.Contract                 { return testActorContract() }
func (*presentationRecordingProvider) Contract() provider.Contract { return testActorContract() }
func (*capabilityRecordingProvider) Contract() provider.Contract   { return testActorContract() }
func (*capabilityActionProvider) Contract() provider.Contract      { return testActorContract() }
func (*completionRegressionProvider) Contract() provider.Contract  { return testActorContract() }
func (*traceMetadataProvider) Contract() provider.Contract         { return testActorContract() }
