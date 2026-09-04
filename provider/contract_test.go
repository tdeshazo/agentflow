package provider

import "testing"

func TestContractSupportsPortableRequirements(t *testing.T) {
	contract := Contract{
		Version:                   ContractVersionV1,
		Modes:                     []ExecutionMode{ExecutionModeAgent},
		InvocationContextVersions: []string{InvocationContextVersion},
		FilesystemBoundary:        true,
		ExecutionPolicy:           true,
	}
	if err := contract.Supports(Requirements{
		ContractVersion:          ContractVersionV1,
		Modes:                    []ExecutionMode{ExecutionModeAgent},
		InvocationContextVersion: InvocationContextVersion,
		FilesystemBoundary:       true,
		ExecutionPolicy:          true,
	}); err != nil {
		t.Fatalf("Supports() error = %v", err)
	}
	if err := contract.Supports(Requirements{Modes: []ExecutionMode{ExecutionModeNestedWorkflow}}); err == nil {
		t.Fatal("Supports() succeeded for unsupported nested workflow mode")
	}
}

func TestRequirementsRejectUnknownContractVersion(t *testing.T) {
	err := (Requirements{ContractVersion: "agentflow.dev/provider/v999"}).Validate()
	if err == nil {
		t.Fatal("Validate() succeeded for unsupported contract version")
	}
}
