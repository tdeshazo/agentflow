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
