package provider

import (
	"fmt"
	"slices"
)

// ContractVersionV1 is the first stable AgentFlow provider contract.
const ContractVersionV1 = "agentflow.dev/provider/v1"

// ExecutionMode identifies a portable class of execution. A provider declares
// only modes it actually implements; the engine never emulates a missing mode.
type ExecutionMode string

const (
	ExecutionModeAgent          ExecutionMode = "agent"
	ExecutionModeLocalCommand   ExecutionMode = "local-command"
	ExecutionModeRemoteService  ExecutionMode = "remote-service"
	ExecutionModeHuman          ExecutionMode = "human"
	ExecutionModeNestedWorkflow ExecutionMode = "nested-workflow"
)

// Contract describes a provider's stable capabilities. It intentionally names
// semantic execution properties rather than provider-specific flags.
type Contract struct {
	Version                   string          `json:"version" yaml:"version"`
	Modes                     []ExecutionMode `json:"modes" yaml:"modes"`
	InvocationContextVersions []string        `json:"invocationContextVersions" yaml:"invocationContextVersions"`
	FilesystemBoundary        bool            `json:"filesystemBoundary" yaml:"filesystemBoundary"`
	ExecutionPolicy           bool            `json:"executionPolicy" yaml:"executionPolicy"`
}

// Requirements is a portable workflow requirement. A zero value preserves
// compatibility with workflows authored before provider contracts existed.
type Requirements struct {
	ContractVersion          string          `yaml:"contractVersion" json:"contractVersion"`
	Modes                    []ExecutionMode `yaml:"modes" json:"modes"`
	InvocationContextVersion string          `yaml:"invocationContextVersion" json:"invocationContextVersion"`
	FilesystemBoundary       bool            `yaml:"filesystemBoundary" json:"filesystemBoundary"`
	ExecutionPolicy          bool            `yaml:"executionPolicy" json:"executionPolicy"`
}

// IsZero reports whether a workflow made no Stage 7 provider requirement.
func (r Requirements) IsZero() bool {
	return r.ContractVersion == "" && len(r.Modes) == 0 && r.InvocationContextVersion == "" && !r.FilesystemBoundary && !r.ExecutionPolicy
}

// Validate checks contract values independently of a concrete provider.
func (c Contract) Validate() error {
	if c.Version != ContractVersionV1 {
		return fmt.Errorf("unsupported provider contract version %q", c.Version)
	}
	if len(c.Modes) == 0 {
		return fmt.Errorf("provider contract must declare at least one execution mode")
	}
	seen := map[ExecutionMode]bool{}
	for _, mode := range c.Modes {
		if !knownExecutionMode(mode) || seen[mode] {
			return fmt.Errorf("provider contract has invalid or duplicate execution mode %q", mode)
		}
		seen[mode] = true
	}
	if len(c.InvocationContextVersions) == 0 {
		return fmt.Errorf("provider contract must declare invocation context versions")
	}
	seenVersions := map[string]bool{}
	for _, version := range c.InvocationContextVersions {
		if version == "" || seenVersions[version] {
			return fmt.Errorf("provider contract has invalid or duplicate invocation context version %q", version)
		}
		seenVersions[version] = true
	}
	return nil
}

// Validate checks portable workflow requirements before the engine creates a
// workspace or invokes a provider.
func (r Requirements) Validate() error {
	if r.ContractVersion != "" && r.ContractVersion != ContractVersionV1 {
		return fmt.Errorf("unsupported provider contract version %q", r.ContractVersion)
	}
	seen := map[ExecutionMode]bool{}
	for _, mode := range r.Modes {
		if !knownExecutionMode(mode) || seen[mode] {
			return fmt.Errorf("executor requirements have invalid or duplicate execution mode %q", mode)
		}
		seen[mode] = true
	}
	return nil
}

// Supports reports whether this provider can meet the portable requirements.
func (c Contract) Supports(r Requirements) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if r.ContractVersion != "" && c.Version != r.ContractVersion {
		return fmt.Errorf("provider contract version %q does not satisfy %q", c.Version, r.ContractVersion)
	}
	for _, mode := range r.Modes {
		if !slices.Contains(c.Modes, mode) {
			return fmt.Errorf("provider does not support execution mode %q", mode)
		}
	}
	if r.InvocationContextVersion != "" && !slices.Contains(c.InvocationContextVersions, r.InvocationContextVersion) {
		return fmt.Errorf("provider does not support invocation context version %q", r.InvocationContextVersion)
	}
	if r.FilesystemBoundary && !c.FilesystemBoundary {
		return fmt.Errorf("provider does not enforce a filesystem boundary")
	}
	if r.ExecutionPolicy && !c.ExecutionPolicy {
		return fmt.Errorf("provider does not enforce execution policy")
	}
	return nil
}

func knownExecutionMode(mode ExecutionMode) bool {
	switch mode {
	case ExecutionModeAgent, ExecutionModeLocalCommand, ExecutionModeRemoteService, ExecutionModeHuman, ExecutionModeNestedWorkflow:
		return true
	default:
		return false
	}
}

// ContractProvider extends the source-compatible Provider interface with the
// versioned Stage 7 capability contract.
type ContractProvider interface {
	Provider
	Contract() Contract
}

// ContractFor returns a provider contract when the provider has adopted the
// stable extension. Legacy providers remain source-compatible, but cannot
// satisfy explicit Requirements.
func ContractFor(p Provider) (Contract, bool) {
	contractProvider, ok := p.(ContractProvider)
	if !ok {
		return Contract{}, false
	}
	return contractProvider.Contract(), true
}

// VerifyContract performs provider-contract conformance checks without
// invoking an external service. Adapters should run it in their shared
// conformance suite in addition to implementation-specific integration tests.
func VerifyContract(p Provider) error {
	if p == nil {
		return fmt.Errorf("provider is nil")
	}
	if p.Name() == "" {
		return fmt.Errorf("provider name is required")
	}
	contract, ok := ContractFor(p)
	if !ok {
		return fmt.Errorf("provider %q does not implement the versioned contract", p.Name())
	}
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("provider %q contract: %w", p.Name(), err)
	}
	filesystem, filesystemOK := p.(FilesystemBoundaryEnforcer)
	if contract.FilesystemBoundary != (filesystemOK && filesystem.EnforcesFilesystemBoundary()) {
		return fmt.Errorf("provider %q filesystem enforcement claim is inconsistent with its adapter", p.Name())
	}
	policy, policyOK := p.(ExecutionPolicyEnforcer)
	if contract.ExecutionPolicy != (policyOK && policy.EnforcesExecutionPolicy()) {
		return fmt.Errorf("provider %q execution policy claim is inconsistent with its adapter", p.Name())
	}
	return nil
}
