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

func TestV2ContractNegotiatesFreshContextAndHandoff(t *testing.T) {
	contract := Contract{Version: ContractVersionV2, Modes: []ExecutionMode{ExecutionModeAgent}, InvocationContextVersions: []string{InvocationContextVersionV1, InvocationContextVersionV2}, HandoffVersions: []string{HandoffVersionV1}}
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Requirements{ContractVersion: ContractVersionV2, InvocationContextVersion: InvocationContextVersionV2}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestV2ContractSatisfiesExplicitV1RequirementsOnlyThroughAdvertisedV1Capabilities(t *testing.T) {
	contract := Contract{
		Version: ContractVersionV2, Modes: []ExecutionMode{ExecutionModeAgent},
		InvocationContextVersions: []string{InvocationContextVersionV1, InvocationContextVersionV2},
		FilesystemBoundary:        true, ExecutionPolicy: true, HandoffVersions: []string{HandoffVersionV1},
	}
	if err := contract.Supports(Requirements{
		ContractVersion: ContractVersionV1, Modes: []ExecutionMode{ExecutionModeAgent},
		InvocationContextVersion: InvocationContextVersionV1, FilesystemBoundary: true, ExecutionPolicy: true,
	}); err != nil {
		t.Fatalf("v2 contract rejected advertised v1 compatibility: %v", err)
	}
	contract.InvocationContextVersions = []string{InvocationContextVersionV2}
	if err := contract.Supports(Requirements{ContractVersion: ContractVersionV1}); err == nil {
		t.Fatal("v2 contract without invocation-context/v1 satisfied an explicit provider/v1 requirement")
	}
	if err := contract.Supports(Requirements{ContractVersion: ContractVersionV1, InvocationContextVersion: InvocationContextVersionV1}); err == nil {
		t.Fatal("v2 contract satisfied a v1 capability it does not advertise")
	}
	if err := contract.Supports(Requirements{ContractVersion: ContractVersionV1, InvocationContextVersion: InvocationContextVersionV2}); err == nil {
		t.Fatal("provider v1 requirement negotiated a provider v2-only context capability")
	}
}

func TestContractVersionCompatibilityDoesNotAcceptUnknownOrDowngradeV2(t *testing.T) {
	v1 := Contract{Version: ContractVersionV1, Modes: []ExecutionMode{ExecutionModeAgent}, InvocationContextVersions: []string{InvocationContextVersionV1}}
	if err := v1.Supports(Requirements{ContractVersion: ContractVersionV2}); err == nil {
		t.Fatal("provider v1 satisfied provider v2 requirement")
	}
	if err := v1.Supports(Requirements{ContractVersion: "agentflow.dev/provider/v999"}); err == nil {
		t.Fatal("unknown requirement version was accepted")
	}
}
