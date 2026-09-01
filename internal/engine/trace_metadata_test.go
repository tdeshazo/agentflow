package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/executiontrace"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type traceMetadataProvider struct {
	result provider.Result
	action func(context.Context, provider.Request) error
}

func (p *traceMetadataProvider) Name() string                     { return "metadata-test" }
func (p *traceMetadataProvider) EnforcesFilesystemBoundary() bool { return true }
func (p *traceMetadataProvider) EnforcesExecutionPolicy() bool    { return true }
func (p *traceMetadataProvider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	if p.action != nil {
		if err := p.action(ctx, request); err != nil {
			return p.result, err
		}
	}
	return p.result, nil
}

func TestProviderAndToolTraceMetadataExcludesPrivateContent(t *testing.T) {
	repo := newDurableRepo(t)
	const (
		credentialValue = "credential-secret-cobalt"
		finalMessage    = "provider-private-message-scarlet"
		prompt          = "prompt-private-content-violet"
		reasoning       = "reasoning-private-content-indigo"
		toolOutput      = "tool-private-output-copper"
	)
	t.Setenv("AGENTFLOW_TRACE_CREDENTIAL", credentialValue)
	w := durableWorkflow(repo, "provider-tool-trace-metadata")
	w.Spec.Execution.Policy = workflow.ExecutionPolicy{
		Network:      "deny",
		ApprovalGate: "provider-approval",
		Credentials: []workflow.CredentialScope{{
			Name: "trace-credential", Env: "AGENTFLOW_TRACE_CREDENTIAL",
		}},
		Budgets: workflow.ResourceBudgets{Tokens: 100, Duration: "1m", CostUSD: 10, ToolCalls: 20},
	}
	w.Spec.Flow = []workflow.FlowStep{{Human: "provider-approval"}, {Phase: "change"}, {Complete: "done"}}
	w.Spec.HumanGates = []workflow.HumanGate{{
		ID:              "provider-approval",
		Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "approve"},
	}}
	w.Spec.Agents["worker"] = workflow.Agent{
		Runner: "test", Model: "trace-model-v1", Sandbox: "workspace-write", Approval: "never",
		Ephemeral: true, MayCommit: true, OutputLastMessage: true,
	}
	w.Spec.Phases[0].Prompt = prompt
	w.Spec.Phases[0].Reasoning = reasoning
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf '" + toolOutput + "'; grep -qx complete work.txt"}
	w.Spec.Tools["skipped"] = workflow.Tool{Type: "shell", Command: "exit 99"}
	validation := w.Spec.Validation["phaseGate"]
	validation.Steps = append([]workflow.ToolUse{{Uses: "skipped", If: "{{ false }}"}}, validation.Steps...)
	w.Spec.Validation["phaseGate"] = validation
	providerImpl := &traceMetadataProvider{
		result: provider.Result{
			FinalMessage: finalMessage,
			Usage:        provider.Usage{InputTokens: 11, OutputTokens: 7, CostUSD: 0.25},
		},
		action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		},
	}
	e := newDurableEngine(t, w, providerImpl)
	e.In = strings.NewReader("approve\n")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := readExecutionTrace(t, e)
	requireTraceEvent(t, events, "provider_request", func(event executiontrace.Event) bool {
		fields := event.Fields
		return fields["provider"] == "metadata-test" && fields["actor"] == "worker" &&
			fields["role"] == invocationRolePhase && fields["context_version"] == provider.InvocationContextVersion &&
			fields["model_config"] == "static" && fields["model_ref"] == opaqueTraceMetadata("model", "trace-model-v1") &&
			fields["sandbox"] == "workspace-write" && fields["approval"] == "never" &&
			fields["ephemeral"] == "true" && fields["network"] == "deny" &&
			fields["credential_count"] == "1" && fields["filesystem_rule_count"] != "0" &&
			fields["token_budget"] == "100" && fields["cost_budget_usd"] == "10" &&
			fields["duration_budget_ms"] == "60000"
	})
	requireTraceEvent(t, events, "provider_response", func(event executiontrace.Event) bool {
		fields := event.Fields
		_, durationErr := strconv.ParseInt(fields["duration_ms"], 10, 64)
		return fields["provider"] == "metadata-test" && fields["actor"] == "worker" &&
			fields["result"] == "success" && fields["input_tokens"] == "11" &&
			fields["output_tokens"] == "7" && fields["cost_usd"] == "0.25" &&
			fields["final_message_present"] == "true" && durationErr == nil
	})
	requireTraceEvent(t, events, "tool_end", func(event executiontrace.Event) bool {
		fields := event.Fields
		_, durationErr := strconv.ParseInt(fields["duration_ms"], 10, 64)
		return fields["tool"] == "gate" && fields["tool_type"] == "shell" &&
			fields["mutates_workspace"] == "false" && fields["result"] == "success" && durationErr == nil
	})
	requireTraceEvent(t, events, "tool_skipped", func(event executiontrace.Event) bool {
		return event.Fields["tool"] == "skipped" && event.Fields["tool_type"] == "shell" && event.Fields["reason"] == "condition_false"
	})

	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{credentialValue, finalMessage, prompt, reasoning, toolOutput, "trace-model-v1"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("trace persisted private provider/tool content %q", private)
		}
	}
}

func TestProviderRequestTraceMetadataDoesNotHashDynamicModelValues(t *testing.T) {
	const dynamicModel = "customer-private-model-value"
	fields := providerRequestTraceFields(
		"test",
		"worker",
		workflow.Agent{Model: "{{ parameters.model }}"},
		provider.Request{Model: dynamicModel},
		PendingActorInvocation{Role: "phase"},
	)
	if fields["model_config"] != "dynamic" {
		t.Fatalf("model config = %q, want dynamic", fields["model_config"])
	}
	if _, ok := fields["model_ref"]; ok {
		t.Fatalf("dynamic model reference was persisted: %#v", fields)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), dynamicModel) || strings.Contains(string(encoded), opaqueTraceMetadata("model", dynamicModel)) {
		t.Fatalf("dynamic model value was recoverable from trace metadata: %s", encoded)
	}
}

func TestToolTraceMetadataRecordsExitCodeWithoutCommandOutput(t *testing.T) {
	repo := newDurableRepo(t)
	const toolOutput = "failing-tool-private-output-ochre"
	w := durableWorkflow(repo, "tool-trace-failure-metadata")
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf '" + toolOutput + "'; exit 7"}
	e := newDurableEngine(t, w, &traceMetadataProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}})
	if err := e.Run(context.Background()); err == nil {
		t.Fatal("run unexpectedly accepted a failing tool")
	}
	events := readExecutionTrace(t, e)
	requireTraceEvent(t, events, "tool_end", func(event executiontrace.Event) bool {
		return event.Fields["tool"] == "gate" && event.Fields["tool_type"] == "shell" &&
			event.Fields["result"] == "failure" && event.Fields["exit_code"] == "7"
	})
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), toolOutput) {
		t.Fatal("trace persisted failing command output")
	}
}
