package workflow

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// EffectiveExecutionPolicy applies an optional executor-local narrowing to a
// workflow policy. It rejects every attempted authority or budget expansion.
func EffectiveExecutionPolicy(base ExecutionPolicy, local *ExecutionPolicy) (ExecutionPolicy, error) {
	base = normalizeExecutionPolicy(base)
	if local == nil {
		return base, nil
	}
	out := base
	if local.Network != "" {
		if base.Network == "deny" && local.Network != "deny" {
			return ExecutionPolicy{}, fmt.Errorf("network %q broadens inherited network %q", local.Network, base.Network)
		}
		out.Network = local.Network
	}
	if local.Capabilities != nil {
		for _, capability := range local.Capabilities {
			if !slices.Contains(base.Capabilities, capability) {
				return ExecutionPolicy{}, fmt.Errorf("capability %q is not granted by the inherited policy", capability)
			}
		}
		out.Capabilities = append([]string(nil), local.Capabilities...)
	}
	if local.Credentials != nil {
		for _, credential := range local.Credentials {
			if !slices.Contains(base.Credentials, credential) {
				return ExecutionPolicy{}, fmt.Errorf("credential %q is not granted by the inherited policy", credential.Name)
			}
		}
		out.Credentials = append([]CredentialScope(nil), local.Credentials...)
	}
	if local.ApprovalGate != "" {
		if base.ApprovalGate != "" && local.ApprovalGate != base.ApprovalGate {
			return ExecutionPolicy{}, fmt.Errorf("approval gate %q replaces inherited gate %q", local.ApprovalGate, base.ApprovalGate)
		}
		out.ApprovalGate = local.ApprovalGate
	}
	var err error
	out.Budgets, err = narrowBudgets(base.Budgets, local.Budgets)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	if out.Network == "deny" && len(out.Capabilities) == 0 && len(out.Credentials) == 0 {
		out.ApprovalGate = ""
	}
	return out, nil
}

// ValidateExecutionPolicy checks the complete normalized workflow contract.
func ValidateExecutionPolicy(w *Workflow) error {
	if w == nil {
		return fmt.Errorf("empty workflow")
	}
	gates := make(map[string]HumanGate, len(w.Spec.HumanGates))
	for _, gate := range w.Spec.HumanGates {
		gates[gate.ID] = gate
	}
	if err := validatePolicy("spec.execution.policy", w.Spec.Execution.Policy, gates); err != nil {
		return err
	}
	for _, name := range sortedKeys(w.Spec.Agents) {
		agent := w.Spec.Agents[name]
		if agent.Policy == nil {
			continue
		}
		path := "spec.agents." + name + ".policy"
		if agent.Policy.Budgets.ToolCalls != 0 {
			return fmt.Errorf("%s.budgets.toolCalls: is workflow-scoped and cannot be overridden per executor", path)
		}
		effective, err := EffectiveExecutionPolicy(w.Spec.Execution.Policy, agent.Policy)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := validatePolicy(path, effective, gates); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRuntimeRecordNames protects the runtime-private state namespace for
// programmatically constructed legacy workflows that bypass document checks.
func ValidateRuntimeRecordNames(w *Workflow) error {
	if w == nil {
		return fmt.Errorf("empty workflow")
	}
	records := w.Spec.State.Records
	configured := []string{records.BaseCommit, records.Branch, records.ActivePhase, records.CompletedPhasePattern, records.CompletedPhases, records.ManualConfirmation, records.HumanVerification, records.WorkflowComplete}
	for _, record := range records.Integrity {
		configured = append(configured, record)
	}
	for _, record := range configured {
		normalized := strings.TrimPrefix(record, "/")
		if normalized == runOwnerRecord || strings.HasPrefix(normalized, "runtime/") {
			return fmt.Errorf("workflow state record %q conflicts with reserved runtime controls", record)
		}
	}
	return nil
}

func normalizeExecutionPolicy(policy ExecutionPolicy) ExecutionPolicy {
	if policy.Network == "" {
		policy.Network = "deny"
	}
	policy.Capabilities = append([]string(nil), policy.Capabilities...)
	policy.Credentials = append([]CredentialScope(nil), policy.Credentials...)
	return policy
}

// ExecutionPolicyRequiresApproval reports whether a policy grants effects that
// must be authorized by durable human evidence before an actor can run.
func ExecutionPolicyRequiresApproval(policy ExecutionPolicy) bool {
	policy = normalizeExecutionPolicy(policy)
	return policy.Network == "allow" || len(policy.Capabilities) != 0 || len(policy.Credentials) != 0
}

func validatePolicy(path string, policy ExecutionPolicy, gates map[string]HumanGate) error {
	policy = normalizeExecutionPolicy(policy)
	if policy.Network != "deny" && policy.Network != "allow" {
		return fmt.Errorf("%s.network: must be deny or allow", path)
	}
	seenCapabilities := map[string]bool{}
	for _, capability := range policy.Capabilities {
		if !identifierPattern.MatchString(capability) || seenCapabilities[capability] {
			return fmt.Errorf("%s.capabilities: invalid or duplicate capability %q", path, capability)
		}
		seenCapabilities[capability] = true
	}
	seenCredentials := map[string]bool{}
	seenTargets := map[string]bool{}
	reservedTargets := map[string]bool{"PATH": true, "HOME": true, "CODEX_HOME": true, "TMPDIR": true}
	for _, credential := range policy.Credentials {
		if !identifierPattern.MatchString(credential.Name) || seenCredentials[credential.Name] {
			return fmt.Errorf("%s.credentials: invalid or duplicate credential name %q", path, credential.Name)
		}
		if !validEnvironmentName(credential.Env) || seenTargets[credential.Env] {
			return fmt.Errorf("%s.credentials: invalid or duplicate target environment %q", path, credential.Env)
		}
		if reservedTargets[credential.Env] {
			return fmt.Errorf("%s.credentials: environment %q is reserved for provider bootstrap", path, credential.Env)
		}
		seenCredentials[credential.Name] = true
		seenTargets[credential.Env] = true
	}
	if err := validateBudgets(path+".budgets", policy.Budgets); err != nil {
		return err
	}
	privileged := ExecutionPolicyRequiresApproval(policy)
	if privileged && policy.ApprovalGate == "" {
		return fmt.Errorf("%s.approvalGate: is required for privileged effects", path)
	}
	if policy.ApprovalGate != "" {
		gate, ok := gates[policy.ApprovalGate]
		if !ok {
			return fmt.Errorf("%s.approvalGate: unknown human gate %q", path, policy.ApprovalGate)
		}
		if err := validatePolicyApprovalGate(gate); err != nil {
			return fmt.Errorf("%s.approvalGate: %w", path, err)
		}
	}
	return nil
}

func validatePolicyApprovalGate(gate HumanGate) error {
	if len(gate.Requires) != 0 || len(gate.After) != 0 {
		return fmt.Errorf("human gate %q must run before phases and cannot declare requires or after", gate.ID)
	}
	if gate.When != "" || gate.If != "" || gate.Skip.AllowedWhen != "" || gate.Skip.Record != "" || gate.Skip.Evidence.Record != "" {
		return fmt.Errorf("human gate %q must be unconditional and cannot be skipped", gate.ID)
	}
	if gate.IdempotentRecord != "" || gate.Evidence.Record != "" {
		return fmt.Errorf("human gate %q must use its canonical durable evidence record", gate.ID)
	}
	return nil
}

func validateBudgets(path string, budgets ResourceBudgets) error {
	if budgets.ModelCalls < 0 || budgets.ToolCalls < 0 || budgets.Tokens < 0 || budgets.CostUSD < 0 || math.IsNaN(budgets.CostUSD) || math.IsInf(budgets.CostUSD, 0) {
		return fmt.Errorf("%s: values must be finite and non-negative", path)
	}
	if strings.TrimSpace(budgets.Duration) != "" {
		duration, err := time.ParseDuration(budgets.Duration)
		if err != nil || duration <= 0 {
			return fmt.Errorf("%s.duration: must be a positive Go duration", path)
		}
	}
	return nil
}

func narrowBudgets(base, local ResourceBudgets) (ResourceBudgets, error) {
	out := base
	if err := validateBudgets("budgets", local); err != nil {
		return ResourceBudgets{}, err
	}
	if err := narrowInt("modelCalls", base.ModelCalls, local.ModelCalls, &out.ModelCalls); err != nil {
		return ResourceBudgets{}, err
	}
	if err := narrowInt("toolCalls", base.ToolCalls, local.ToolCalls, &out.ToolCalls); err != nil {
		return ResourceBudgets{}, err
	}
	if local.Tokens != 0 {
		if base.Tokens != 0 && local.Tokens > base.Tokens {
			return ResourceBudgets{}, fmt.Errorf("tokens budget %d exceeds inherited limit %d", local.Tokens, base.Tokens)
		}
		out.Tokens = local.Tokens
	}
	if local.CostUSD != 0 {
		if base.CostUSD != 0 && local.CostUSD > base.CostUSD {
			return ResourceBudgets{}, fmt.Errorf("costUSD budget %g exceeds inherited limit %g", local.CostUSD, base.CostUSD)
		}
		out.CostUSD = local.CostUSD
	}
	if local.Duration != "" {
		localDuration, _ := time.ParseDuration(local.Duration)
		if base.Duration != "" {
			baseDuration, _ := time.ParseDuration(base.Duration)
			if localDuration > baseDuration {
				return ResourceBudgets{}, fmt.Errorf("duration budget %s exceeds inherited limit %s", local.Duration, base.Duration)
			}
		}
		out.Duration = local.Duration
	}
	return out, nil
}

func narrowInt(name string, base, local int, target *int) error {
	if local == 0 {
		return nil
	}
	if base != 0 && local > base {
		return fmt.Errorf("%s budget %d exceeds inherited limit %d", name, local, base)
	}
	*target = local
	return nil
}
