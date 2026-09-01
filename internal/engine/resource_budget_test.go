package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestRedactExecutionErrorPreservesClassification(t *testing.T) {
	cause := &budgetExhaustedError{resource: "secret-value"}
	err := redactExecutionError(fmt.Errorf("provider exposed secret-value: %w", cause), map[string]string{"TOKEN": "secret-value"})
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("secret survived redaction: %v", err)
	}
	var exhausted *budgetExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("redaction lost error classification: %v", err)
	}
}

func TestResourceBudgetPersistsExhaustionBeforeRetry(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "budget"}, Spec: workflow.Spec{
		Execution: workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{ModelCalls: 1}}},
		Agents:    map[string]workflow.Agent{"worker": {Runner: "test"}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	ctx, cancel, err := e.prepareResourceContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = ctx
	policy, _ := e.effectiveExecutionPolicy(e.Workflow.Spec.Agents["worker"])
	if _, err := e.reserveModelCall("worker", policy); err != nil {
		t.Fatal(err)
	}
	if _, err := e.reserveModelCall("worker", policy); err == nil || !strings.Contains(err.Error(), "modelCalls") {
		t.Fatalf("second reservation error = %v", err)
	}

	restarted := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	if _, _, err := restarted.prepareResourceContext(context.Background()); err == nil || !strings.Contains(err.Error(), "modelCalls") {
		t.Fatalf("restart error = %v", err)
	}
}

func TestResourceBudgetAccountsProviderUsage(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "tokens"}, Spec: workflow.Spec{
		Execution: workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{Tokens: 10}}},
		Agents:    map[string]workflow.Agent{"worker": {Runner: "test"}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	if _, cancel, err := e.prepareResourceContext(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		cancel()
	}
	policy, _ := e.effectiveExecutionPolicy(e.Workflow.Spec.Agents["worker"])
	if _, err := e.reserveModelCall("worker", policy); err != nil {
		t.Fatal(err)
	}
	err := e.recordModelUsage("worker", policy, provider.Usage{InputTokens: 8, OutputTokens: 3})
	var exhausted *budgetExhaustedError
	if !errors.As(err, &exhausted) || exhausted.resource != "tokens" {
		t.Fatalf("usage error = %v", err)
	}
	var usage ResourceUsage
	if ok, err := e.Store.GetJSON(resourceUsageRecord, &usage); err != nil || !ok || usage.Exhausted != "tokens" || usage.Tokens != 11 {
		t.Fatalf("usage = %#v, ok=%v err=%v", usage, ok, err)
	}
	head, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetCommit(e.baseRecord(), head); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.branchRecord(), "main"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := e.statusSnapshot()
	if err != nil || snapshot.State != "budget-exhausted/terminal" || snapshot.BudgetExhausted != "tokens" {
		t.Fatalf("status = %#v, err=%v", snapshot, err)
	}
}

func TestResourceBudgetAccountsAgentDurationAcrossInvocations(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "actor-duration"}, Spec: workflow.Spec{
		Execution: workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{Duration: "2h"}}},
		Agents: map[string]workflow.Agent{"worker": {
			Runner: "test",
			Policy: &workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{Duration: "1h"}},
		}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	policy, err := e.effectiveExecutionPolicy(e.Workflow.Spec.Agents["worker"])
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.reserveModelCall("worker", policy)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := e.recordModelUsage("worker", policy, provider.Usage{}); err != nil {
		t.Fatal(err)
	}
	var usage ResourceUsage
	if ok, err := e.Store.GetJSON(resourceUsageRecord, &usage); err != nil || !ok {
		t.Fatalf("load duration usage: ok=%v err=%v", ok, err)
	}
	if actorUsage := usage.Actors["worker"]; actorUsage.Duration < 10*time.Millisecond || !actorUsage.DurationStartedAt.IsZero() {
		t.Fatalf("actor duration usage = %#v", actorUsage)
	}

	restarted := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	second, err := restarted.reserveModelCall("worker", policy)
	if err != nil {
		t.Fatal(err)
	}
	if second.Duration <= 0 || second.Duration >= first.Duration {
		t.Fatalf("second duration = %s, want positive remaining duration below %s", time.Duration(second.Duration), time.Duration(first.Duration))
	}
}

func TestResourceBudgetClassifiesDurationExhaustion(t *testing.T) {
	tests := []struct {
		name           string
		globalDuration string
		actorDuration  string
		want           string
	}{
		{name: "narrowed actor deadline", globalDuration: "1h", actorDuration: "20ms", want: "duration/worker"},
		{name: "workflow deadline", globalDuration: "20ms", want: "duration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newEngineOwnedRepo(t)
			agent := workflow.Agent{Runner: "test"}
			if test.actorDuration != "" {
				agent.Policy = &workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{Duration: test.actorDuration}}
			}
			w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "duration-timeout"}, Spec: workflow.Spec{
				Execution: workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{Duration: test.globalDuration}}},
				Agents:    map[string]workflow.Agent{"worker": agent},
			}}
			p := &capabilityActionProvider{action: func(ctx context.Context, _ provider.Request) error {
				<-ctx.Done()
				return ctx.Err()
			}}
			e := newCapabilityEngine(t, w, repo, p)
			runCtx, cancel, err := e.prepareResourceContext(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			runErr := e.runAgent(runCtx, "worker", "", "wait", nil)
			var exhausted *budgetExhaustedError
			if !errors.As(runErr, &exhausted) || exhausted.resource != test.want {
				t.Fatalf("actor invocation error = %v, want exhausted %q", runErr, test.want)
			}
			var usage ResourceUsage
			if ok, err := e.Store.GetJSON(resourceUsageRecord, &usage); err != nil || !ok {
				t.Fatalf("load duration usage: ok=%v err=%v", ok, err)
			}
			if usage.Exhausted != test.want {
				t.Fatalf("exhausted resource = %q, want %q", usage.Exhausted, test.want)
			}
		})
	}
}

func TestToolCallBudgetIsConsumedBeforeExecution(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "tools"}, Spec: workflow.Spec{
		Execution: workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Budgets: workflow.ResourceBudgets{ToolCalls: 1}}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	if _, cancel, err := e.prepareResourceContext(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		cancel()
	}
	if err := e.consumeToolCall("validation"); err != nil {
		t.Fatal(err)
	}
	if err := e.consumeToolCall("validation"); err == nil || !strings.Contains(err.Error(), "toolCalls/validation") {
		t.Fatalf("second tool call error = %v", err)
	}
}

func TestExecutionPolicyRequiresDurableApprovalAndExplicitCredentials(t *testing.T) {
	t.Setenv("DEPLOY_TOKEN", "top-secret")
	repo := newEngineOwnedRepo(t)
	policy := workflow.ExecutionPolicy{
		Network: "allow", Credentials: []workflow.CredentialScope{{Name: "deploy", Env: "DEPLOY_TOKEN"}}, ApprovalGate: "review",
	}
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "privileged"}, Spec: workflow.Spec{
		Execution: workflow.ExecutionSpec{Policy: policy}, HumanGates: []workflow.HumanGate{{ID: "review"}}, Agents: map[string]workflow.Agent{"worker": {Runner: "test"}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	if err := e.authorizeExecutionPolicy(policy); err == nil {
		t.Fatal("privileged policy ran without human approval")
	}
	head, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetCommit("human/review", head); err != nil {
		t.Fatal(err)
	}
	if err := e.authorizeExecutionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	credentials, err := e.resolveCredentials(policy)
	if err != nil || credentials["DEPLOY_TOKEN"] != "top-secret" {
		t.Fatalf("credentials = %#v, err=%v", credentials, err)
	}
}

func TestExecutionPolicyApprovalIsRecordedBeforeSuccessorActorsRun(t *testing.T) {
	repo := newEngineOwnedRepo(t)
	w := &workflow.Workflow{Metadata: workflow.Metadata{Name: "preauthorize"}, Spec: workflow.Spec{
		Execution:  workflow.ExecutionSpec{Policy: workflow.ExecutionPolicy{Network: "allow", ApprovalGate: "review"}},
		Agents:     map[string]workflow.Agent{"worker": {Runner: "test"}},
		Phases:     []workflow.Phase{{ID: "build", Actor: "worker"}},
		HumanGates: []workflow.HumanGate{{ID: "review", Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "approve"}}},
	}}
	e := newCapabilityEngine(t, w, repo, &capabilityActionProvider{})
	e.In = bytes.NewBufferString("approve\n")
	if err := e.ensureExecutionPolicyApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.authorizeExecutionPolicy(w.Spec.Execution.Policy); err != nil {
		t.Fatalf("approval was not durable before actor authorization: %v", err)
	}
}
