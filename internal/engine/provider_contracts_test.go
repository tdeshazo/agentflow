package engine

import "github.com/tdeshazo/agentflow/provider"

func testActorContract() provider.Contract {
	return provider.Contract{
		Version: provider.ContractVersionV1, Modes: []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersion}, FilesystemBoundary: true, ExecutionPolicy: true,
	}
}

func (*schedulingProvider) Contract() provider.Contract            { return testActorContract() }
func (*checklistProvider) Contract() provider.Contract             { return testActorContract() }
func (*engineOwnedProvider) Contract() provider.Contract           { return testActorContract() }
func (*referenceWorkflowProvider) Contract() provider.Contract     { return testActorContract() }
func (*canonicalSelfHostingProvider) Contract() provider.Contract  { return testActorContract() }
func (*writeProvider) Contract() provider.Contract                 { return testActorContract() }
func (*presentationRecordingProvider) Contract() provider.Contract { return testActorContract() }
func (*capabilityRecordingProvider) Contract() provider.Contract   { return testActorContract() }
func (*capabilityActionProvider) Contract() provider.Contract      { return testActorContract() }
func (*completionRegressionProvider) Contract() provider.Contract  { return testActorContract() }
func (*traceMetadataProvider) Contract() provider.Contract         { return testActorContract() }
