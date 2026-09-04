package engine

import (
	"fmt"
	"slices"
	"sort"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	"github.com/tdeshazo/agentflow/tool"
)

// validateExtensions resolves every explicit provider and tool contract before
// a run can create a worktree, invoke a provider, or execute a plugin.
func (e *Engine) validateExtensions() error {
	if e == nil || e.Workflow == nil {
		return fmt.Errorf("empty engine workflow")
	}
	if !e.stateOnly {
		for _, name := range sortedAgentNames(e.Workflow.Spec.Agents) {
			agent := e.Workflow.Spec.Agents[name]
			providerImpl, ok := e.Providers[agent.Runner]
			if !ok {
				return fmt.Errorf("actor %q requires provider %q, but it is not registered", name, agent.Runner)
			}
			contract, hasContract := provider.ContractFor(providerImpl)
			if hasContract {
				if err := provider.VerifyContract(providerImpl); err != nil {
					return fmt.Errorf("actor %q provider preflight: %w", name, err)
				}
			} else if !agent.Requirements.IsZero() {
				return fmt.Errorf("actor %q provider preflight: provider %q does not implement the versioned contract", name, providerImpl.Name())
			}
			filesystemEnforcer, filesystemOK := providerImpl.(provider.FilesystemBoundaryEnforcer)
			if !filesystemOK || !filesystemEnforcer.EnforcesFilesystemBoundary() {
				return fmt.Errorf("actor %q provider %q cannot enforce the mandatory filesystem boundary", name, providerImpl.Name())
			}
			policyEnforcer, policyOK := providerImpl.(provider.ExecutionPolicyEnforcer)
			if !policyOK || !policyEnforcer.EnforcesExecutionPolicy() {
				return fmt.Errorf("actor %q provider %q cannot enforce the mandatory execution policy", name, providerImpl.Name())
			}
			if !agent.Requirements.IsZero() {
				if !slices.Contains(contract.Modes, provider.ExecutionModeAgent) {
					return fmt.Errorf("actor %q provider %q does not advertise agent execution", name, providerImpl.Name())
				}
				if err := contract.Supports(agent.Requirements); err != nil {
					return fmt.Errorf("actor %q provider %q does not satisfy executor requirements: %w", name, providerImpl.Name(), err)
				}
			}
			invocationModes := e.agentInvocationModes(name)
			if invocationModes.ordinary && hasContract && !slices.Contains(contract.InvocationContextVersions, provider.InvocationContextVersionV1) {
				return fmt.Errorf("actor %q provider %q requires invocation context %q for ordinary phases", name, providerImpl.Name(), provider.InvocationContextVersionV1)
			}
			if invocationModes.structured && (!hasContract || contract.Version != provider.ContractVersionV2 || !slices.Contains(contract.InvocationContextVersions, provider.InvocationContextVersionV2) || !slices.Contains(contract.HandoffVersions, provider.HandoffVersionV1)) {
				return fmt.Errorf("actor %q provider %q requires provider contract %q with invocation context %q and structured handoff %q", name, providerImpl.Name(), provider.ContractVersionV2, provider.InvocationContextVersionV2, provider.HandoffVersionV1)
			}
		}
	}

	configs := make(map[string]any, len(e.Workflow.Spec.Tools))
	for _, name := range sortedToolNames(e.Workflow.Spec.Tools) {
		definition := e.Workflow.Spec.Tools[name]
		if builtinTool(definition.Type) {
			if len(definition.Config) != 0 {
				return fmt.Errorf("tool %q type %q does not accept plugin config", name, definition.Type)
			}
			continue
		}
		plugin, ok := e.ToolRegistry.Lookup(definition.Type)
		if !ok {
			return fmt.Errorf("tool %q requires plugin type %q, but it is not registered", name, definition.Type)
		}
		descriptor := plugin.Descriptor()
		if descriptor.Type != definition.Type {
			return fmt.Errorf("tool %q plugin type %q does not match declaration %q", name, descriptor.Type, definition.Type)
		}
		declaredMutation := tool.MutationNone
		if definition.MutatesWorkspace {
			declaredMutation = tool.MutationWorkspace
		}
		if descriptor.Mutation != declaredMutation {
			return fmt.Errorf("tool %q mutation declaration %q does not match plugin %q", name, declaredMutation, descriptor.Mutation)
		}
		raw := definition.Config
		if raw == nil {
			raw = map[string]any{}
		}
		config, err := plugin.DecodeConfig(raw)
		if err != nil {
			return fmt.Errorf("tool %q typed config: %w", name, err)
		}
		configs[name] = config
	}
	e.toolConfigs = configs
	return nil
}

type invocationModes struct {
	ordinary   bool
	structured bool
}

func (e *Engine) agentInvocationModes(actor string) invocationModes {
	var modes invocationModes
	addValidationRepairMode := func(name string, structured bool) {
		validation, ok := e.Workflow.Spec.Validation[name]
		if !ok || validation.OnFailure.Strategy != "repair-once" || validation.OnFailure.Repair.Actor != actor {
			return
		}
		if structured {
			modes.structured = true
		} else {
			modes.ordinary = true
		}
	}
	for index := range e.Workflow.Spec.Phases {
		phase := &e.Workflow.Spec.Phases[index]
		structured := phaseRequiresStructuredHandoff(phase)
		if phase.Actor == actor {
			if structured {
				modes.structured = true
			} else {
				modes.ordinary = true
			}
		}
		for _, name := range e.selectedPhaseValidations(phase) {
			addValidationRepairMode(name, structured)
		}
		// Before validations are executable repair paths but are intentionally
		// excluded from the phase actor's selected semantic context.
		for _, action := range e.Workflow.Spec.PhaseDefaults.Before {
			if action.Validate != "" {
				addValidationRepairMode(action.Validate, structured)
			}
		}
	}
	for _, name := range e.standaloneValidationNames() {
		addValidationRepairMode(name, false)
	}
	return modes
}

func (e *Engine) standaloneValidationNames() []string {
	names := map[string]bool{}
	for _, step := range e.Workflow.Spec.Flow {
		if step.Validate != "" {
			names[step.Validate] = true
		}
	}
	for _, gate := range e.Workflow.Spec.HumanGates {
		for _, action := range gate.After {
			if action.Validation == "" {
				continue
			}
			name := action.Validation
			if _, ok := e.Workflow.Spec.Validation[name]; !ok {
				for _, step := range e.Workflow.Spec.Flow {
					if step.ID == name && step.Validate != "" {
						name = step.Validate
						break
					}
				}
			}
			names[name] = true
		}
	}
	for _, completion := range e.Workflow.Spec.Completion {
		if completion.FinalValidation != "" {
			names[completion.FinalValidation] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func builtinTool(kind string) bool {
	switch kind {
	case "workspace-policy", "shell", "git-checkpoint", "file-regex", "markdown-checklist-progress":
		return true
	default:
		return false
	}
}

func sortedAgentNames(agents map[string]workflow.Agent) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedToolNames(tools map[string]workflow.Tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
