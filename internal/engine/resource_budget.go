package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

const resourceUsageRecord = "runtime/resource-usage"

type resourceCounters struct {
	ModelCalls        int           `json:"model_calls"`
	Tokens            int64         `json:"tokens"`
	CostUSD           float64       `json:"cost_usd"`
	Duration          time.Duration `json:"duration,omitempty"`
	DurationStartedAt time.Time     `json:"duration_started_at,omitempty"`
}

// ResourceUsage is durable policy state. Calls are reserved before execution,
// so interruption cannot reset a consumed call budget.
type ResourceUsage struct {
	Version    int                         `json:"version"`
	StartedAt  time.Time                   `json:"started_at"`
	ModelCalls int                         `json:"model_calls"`
	ToolCalls  int                         `json:"tool_calls"`
	Tokens     int64                       `json:"tokens"`
	CostUSD    float64                     `json:"cost_usd"`
	Actors     map[string]resourceCounters `json:"actors,omitempty"`
	Exhausted  string                      `json:"exhausted,omitempty"`
}

type budgetExhaustedError struct{ resource string }

func (e *budgetExhaustedError) Error() string {
	return "execution budget exhausted: " + e.resource
}

type redactedExecutionError struct {
	err     error
	message string
}

func (e *redactedExecutionError) Error() string { return e.message }
func (e *redactedExecutionError) Unwrap() error { return e.err }

func (e *Engine) prepareResourceContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if !e.hasResourceBudgets() {
		return ctx, func() {}, nil
	}
	e.resourceMu.Lock()
	defer e.resourceMu.Unlock()
	usage, err := e.loadResourceUsage()
	if err != nil {
		return nil, nil, err
	}
	if usage.Exhausted != "" {
		return nil, nil, &budgetExhaustedError{resource: usage.Exhausted}
	}
	if usage.StartedAt.IsZero() {
		usage.StartedAt = time.Now().UTC()
		if err := e.Store.SetJSON(resourceUsageRecord, usage); err != nil {
			return nil, nil, fmt.Errorf("persist resource budget start: %w", err)
		}
	}
	duration := e.Workflow.Spec.Execution.Policy.Budgets.Duration
	if duration == "" {
		return ctx, func() {}, nil
	}
	limit, _ := time.ParseDuration(duration)
	deadline := usage.StartedAt.Add(limit)
	if !time.Now().Before(deadline) {
		if err := e.exhaustBudgetLocked(&usage, "duration"); err != nil {
			return nil, nil, err
		}
		return nil, nil, &budgetExhaustedError{resource: "duration"}
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	return bounded, cancel, nil
}

func (e *Engine) effectiveExecutionPolicy(agent workflow.Agent) (workflow.ExecutionPolicy, error) {
	return workflow.EffectiveExecutionPolicy(e.Workflow.Spec.Execution.Policy, agent.Policy)
}

func (e *Engine) authorizeExecutionPolicy(policy workflow.ExecutionPolicy) error {
	if policy.ApprovalGate == "" {
		return nil
	}
	if ok, _, err := e.validCommitMarker("human/" + policy.ApprovalGate); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("privileged execution requires completed human gate %q", policy.ApprovalGate)
	}
	return nil
}

// ensureExecutionPolicyApprovals records every approval needed by an actor
// reachable from the successor schedule. It runs before interrupted-phase
// recovery because recovery may invoke either the phase actor or a repair
// actor. Policy validation guarantees these gates have no phase prerequisites,
// conditions, alternate records, or actor-running after actions.
func (e *Engine) ensureExecutionPolicyApprovals(ctx context.Context) error {
	actors := make(map[string]bool)
	for _, phase := range e.Workflow.Spec.Phases {
		actors[phase.Actor] = true
	}
	for _, validation := range e.Workflow.Spec.Validation {
		if validation.OnFailure.Repair.Actor != "" {
			actors[validation.OnFailure.Repair.Actor] = true
		}
	}
	gates := make(map[string]bool)
	for actor := range actors {
		agent, ok := e.Workflow.Spec.Agents[actor]
		if !ok {
			return fmt.Errorf("execution policy references unknown actor %q", actor)
		}
		policy, err := e.effectiveExecutionPolicy(agent)
		if err != nil {
			return fmt.Errorf("execution policy for actor %q: %w", actor, err)
		}
		if workflow.ExecutionPolicyRequiresApproval(policy) {
			gates[policy.ApprovalGate] = true
		}
	}
	gateNames := make([]string, 0, len(gates))
	for gate := range gates {
		gateNames = append(gateNames, gate)
	}
	sort.Strings(gateNames)
	for _, gate := range gateNames {
		e.runStage = "approval/" + gate
		if err := e.runHuman(ctx, gate); err != nil {
			return fmt.Errorf("execution policy approval %q: %w", gate, err)
		}
	}
	return nil
}

func (e *Engine) resolveCredentials(policy workflow.ExecutionPolicy) (map[string]string, error) {
	credentials := make(map[string]string, len(policy.Credentials))
	for _, scope := range policy.Credentials {
		value, ok := os.LookupEnv(scope.Env)
		if !ok {
			return nil, fmt.Errorf("authorized credential %q requires environment %s", scope.Name, scope.Env)
		}
		credentials[scope.Env] = value
	}
	return credentials, nil
}

func redactExecutionError(err error, credentials map[string]string) error {
	if err == nil || len(credentials) == 0 {
		return err
	}
	values := make([]string, 0, len(credentials))
	for _, value := range credentials {
		if value != "" {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	message := err.Error()
	for _, value := range values {
		message = strings.ReplaceAll(message, value, "[REDACTED]")
	}
	if message == err.Error() {
		return err
	}
	return &redactedExecutionError{err: err, message: message}
}

func providerExecutionPolicy(policy workflow.ExecutionPolicy) provider.ExecutionPolicy {
	return provider.ExecutionPolicy{
		Network: policy.Network, Capabilities: append([]string(nil), policy.Capabilities...),
		ApprovalGate: policy.ApprovalGate,
	}
}

func (e *Engine) reserveModelCall(actor string, policy workflow.ExecutionPolicy) (provider.InvocationBudget, error) {
	if !hasModelBudget(e.Workflow.Spec.Execution.Policy.Budgets) && !hasModelBudget(policy.Budgets) {
		budget := provider.InvocationBudget{}
		if policy.Budgets.Duration != "" {
			duration, _ := time.ParseDuration(policy.Budgets.Duration)
			budget.Duration = int64(duration)
		}
		return budget, nil
	}
	e.resourceMu.Lock()
	defer e.resourceMu.Unlock()
	usage, err := e.loadResourceUsage()
	if err != nil {
		return provider.InvocationBudget{}, err
	}
	if usage.Exhausted != "" {
		return provider.InvocationBudget{}, &budgetExhaustedError{resource: usage.Exhausted}
	}
	global := e.Workflow.Spec.Execution.Policy.Budgets
	actorUsage := usage.Actors[actor]
	actorDuration := e.actorDurationLimit(actor)
	if actorDuration > 0 {
		consumeActorDuration(&actorUsage, time.Now().UTC())
		if actorUsage.Duration >= actorDuration {
			usage.Actors[actor] = actorUsage
			return provider.InvocationBudget{}, e.markBudgetExhaustedLocked(&usage, "duration/"+actor)
		}
	}
	if global.ModelCalls > 0 && usage.ModelCalls >= global.ModelCalls {
		return provider.InvocationBudget{}, e.markBudgetExhaustedLocked(&usage, "modelCalls")
	}
	if policy.Budgets.ModelCalls > 0 && actorUsage.ModelCalls >= policy.Budgets.ModelCalls {
		return provider.InvocationBudget{}, e.markBudgetExhaustedLocked(&usage, "modelCalls/"+actor)
	}
	if (global.Tokens > 0 && usage.Tokens >= global.Tokens) || (policy.Budgets.Tokens > 0 && actorUsage.Tokens >= policy.Budgets.Tokens) {
		return provider.InvocationBudget{}, e.markBudgetExhaustedLocked(&usage, "tokens")
	}
	if (global.CostUSD > 0 && usage.CostUSD >= global.CostUSD) || (policy.Budgets.CostUSD > 0 && actorUsage.CostUSD >= policy.Budgets.CostUSD) {
		return provider.InvocationBudget{}, e.markBudgetExhaustedLocked(&usage, "costUSD")
	}
	usage.ModelCalls++
	actorUsage.ModelCalls++
	if actorDuration > 0 {
		actorUsage.DurationStartedAt = time.Now().UTC()
	}
	usage.Actors[actor] = actorUsage
	if err := e.Store.SetJSON(resourceUsageRecord, usage); err != nil {
		return provider.InvocationBudget{}, fmt.Errorf("reserve model-call budget: %w", err)
	}
	budget := provider.InvocationBudget{
		Tokens: minPositiveInt64(
			remainingInt64(global.Tokens, usage.Tokens),
			remainingInt64(policy.Budgets.Tokens, actorUsage.Tokens),
		),
		CostUSD: minPositiveFloat(
			remainingFloat(global.CostUSD, usage.CostUSD),
			remainingFloat(policy.Budgets.CostUSD, actorUsage.CostUSD),
		),
	}
	if policy.Budgets.Duration != "" {
		duration, _ := time.ParseDuration(policy.Budgets.Duration)
		if actorDuration > 0 {
			duration = remainingDuration(actorDuration, actorUsage.Duration)
		}
		budget.Duration = int64(duration)
	}
	return budget, nil
}

func (e *Engine) recordModelUsage(actor string, policy workflow.ExecutionPolicy, reported provider.Usage) error {
	if !hasModelBudget(e.Workflow.Spec.Execution.Policy.Budgets) && !hasModelBudget(policy.Budgets) {
		return nil
	}
	e.resourceMu.Lock()
	defer e.resourceMu.Unlock()
	usage, err := e.loadResourceUsage()
	if err != nil {
		return err
	}
	tokens := reported.InputTokens + reported.OutputTokens
	if (policy.Budgets.Tokens > 0 || policy.Budgets.CostUSD > 0) && tokens == 0 && reported.CostUSD == 0 {
		return e.markBudgetExhaustedLocked(&usage, "unreported-provider-usage")
	}
	usage.Tokens += tokens
	usage.CostUSD += reported.CostUSD
	actorUsage := usage.Actors[actor]
	actorDuration := e.actorDurationLimit(actor)
	if actorDuration > 0 {
		consumeActorDuration(&actorUsage, time.Now().UTC())
	}
	actorUsage.Tokens += tokens
	actorUsage.CostUSD += reported.CostUSD
	usage.Actors[actor] = actorUsage
	global := e.Workflow.Spec.Execution.Policy.Budgets
	resource := ""
	switch {
	case actorDuration > 0 && actorUsage.Duration >= actorDuration:
		resource = "duration/" + actor
	case exceedsInt64(global.Tokens, usage.Tokens) || exceedsInt64(policy.Budgets.Tokens, actorUsage.Tokens):
		resource = "tokens"
	case exceedsFloat(global.CostUSD, usage.CostUSD) || exceedsFloat(policy.Budgets.CostUSD, actorUsage.CostUSD):
		resource = "costUSD"
	}
	if resource != "" {
		if err := e.exhaustBudgetLocked(&usage, resource); err != nil {
			return err
		}
		return &budgetExhaustedError{resource: resource}
	}
	return e.Store.SetJSON(resourceUsageRecord, usage)
}

func (e *Engine) consumeToolCall(name string) error {
	limit := e.Workflow.Spec.Execution.Policy.Budgets.ToolCalls
	if limit == 0 {
		return nil
	}
	e.resourceMu.Lock()
	defer e.resourceMu.Unlock()
	usage, err := e.loadResourceUsage()
	if err != nil {
		return err
	}
	if usage.Exhausted != "" {
		return &budgetExhaustedError{resource: usage.Exhausted}
	}
	if usage.ToolCalls >= limit {
		return e.markBudgetExhaustedLocked(&usage, "toolCalls/"+name)
	}
	usage.ToolCalls++
	return e.Store.SetJSON(resourceUsageRecord, usage)
}

func (e *Engine) loadResourceUsage() (ResourceUsage, error) {
	usage := ResourceUsage{Version: 1, Actors: map[string]resourceCounters{}}
	ok, err := e.Store.GetJSON(resourceUsageRecord, &usage)
	if err != nil {
		return ResourceUsage{}, err
	}
	if ok && usage.Version != 1 {
		return ResourceUsage{}, fmt.Errorf("unsupported resource usage version %d", usage.Version)
	}
	if usage.Actors == nil {
		usage.Actors = map[string]resourceCounters{}
	}
	return usage, nil
}

func (e *Engine) markBudgetExhaustedLocked(usage *ResourceUsage, resource string) error {
	if err := e.exhaustBudgetLocked(usage, resource); err != nil {
		return err
	}
	return &budgetExhaustedError{resource: resource}
}

func (e *Engine) exhaustBudgetLocked(usage *ResourceUsage, resource string) error {
	usage.Exhausted = resource
	if err := e.Store.SetJSON(resourceUsageRecord, *usage); err != nil {
		return fmt.Errorf("persist exhausted %s budget: %w", resource, err)
	}
	return nil
}

func remainingInt64(limit, used int64) int64 {
	if limit == 0 {
		return 0
	}
	if used >= limit {
		return 1
	}
	return limit - used
}

func remainingDuration(limit, used time.Duration) time.Duration {
	if used >= limit {
		return 1
	}
	return limit - used
}

func consumeActorDuration(usage *resourceCounters, now time.Time) {
	if usage.DurationStartedAt.IsZero() {
		return
	}
	if elapsed := now.Sub(usage.DurationStartedAt); elapsed > 0 {
		usage.Duration += elapsed
	}
	usage.DurationStartedAt = time.Time{}
}

func (e *Engine) actorDurationLimit(actor string) time.Duration {
	agent, ok := e.Workflow.Spec.Agents[actor]
	if !ok || agent.Policy == nil || agent.Policy.Budgets.Duration == "" {
		return 0
	}
	duration, _ := time.ParseDuration(agent.Policy.Budgets.Duration)
	return duration
}

func remainingFloat(limit, used float64) float64 {
	if limit == 0 {
		return 0
	}
	return math.Max(0, limit-used)
}

func minPositiveInt64(left, right int64) int64 {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}

func minPositiveFloat(left, right float64) float64 {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}

func exceedsInt64(limit, used int64) bool   { return limit > 0 && used > limit }
func exceedsFloat(limit, used float64) bool { return limit > 0 && used > limit+1e-9 }

func hasModelBudget(budgets workflow.ResourceBudgets) bool {
	return budgets.ModelCalls > 0 || budgets.Tokens > 0 || budgets.CostUSD > 0 || budgets.Duration != ""
}

func (e *Engine) hasResourceBudgets() bool {
	budgets := e.Workflow.Spec.Execution.Policy.Budgets
	if hasModelBudget(budgets) || budgets.ToolCalls > 0 {
		return true
	}
	for _, agent := range e.Workflow.Spec.Agents {
		if agent.Policy != nil && hasModelBudget(agent.Policy.Budgets) {
			return true
		}
	}
	return false
}

func (e *Engine) workflowDurationBudgetExpired() bool {
	configured := e.Workflow.Spec.Execution.Policy.Budgets.Duration
	if configured == "" {
		return false
	}
	usage, err := e.loadResourceUsage()
	if err != nil || usage.StartedAt.IsZero() {
		return false
	}
	limit, err := time.ParseDuration(configured)
	return err == nil && !time.Now().Before(usage.StartedAt.Add(limit))
}
