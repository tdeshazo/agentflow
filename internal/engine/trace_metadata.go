package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

// opaqueTraceRecord keeps resolved parameter and environment values out of the
// non-authoritative execution trace while preserving a stable reference that
// can correlate events for the same Git-backed record.
func opaqueTraceRecord(record string) string {
	digest := sha256.Sum256([]byte(record))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func opaqueTraceMetadata(kind, value string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func providerRequestTraceFields(providerName, actor string, agent workflow.Agent, request provider.Request, invocation PendingActorInvocation) map[string]string {
	fields := map[string]string{
		"actor":                 actor,
		"approval":              request.Approval,
		"capability_count":      strconv.Itoa(len(request.Policy.Capabilities)),
		"context_version":       request.Context.Version,
		"credential_count":      strconv.Itoa(len(request.Credentials)),
		"ephemeral":             strconv.FormatBool(request.Ephemeral),
		"filesystem_rule_count": strconv.Itoa(len(request.FilesystemBoundary)),
		"network":               request.Policy.Network,
		"output_capture":        strconv.FormatBool(request.OutputLastMessage),
		"presentation":          string(request.Presentation),
		"provider":              providerName,
		"role":                  request.Context.Invocation.Role,
		"sandbox":               request.Sandbox,
	}
	if fields["role"] == "" {
		fields["role"] = invocation.Role
	}
	switch {
	case agent.Model == "":
		fields["model_config"] = "default"
	case strings.Contains(agent.Model, "{{"):
		// Expanded model values may contain parameter or environment data. Keep
		// only their dynamic/static classification in the durable trace.
		fields["model_config"] = "dynamic"
	default:
		fields["model_config"] = "static"
		fields["model_ref"] = opaqueTraceMetadata("model", request.Model)
	}
	if request.Budget.Tokens > 0 {
		fields["token_budget"] = strconv.FormatInt(request.Budget.Tokens, 10)
	}
	if request.Budget.Duration > 0 {
		fields["duration_budget_ms"] = strconv.FormatInt(time.Duration(request.Budget.Duration).Milliseconds(), 10)
	}
	if request.Budget.CostUSD > 0 {
		fields["cost_budget_usd"] = strconv.FormatFloat(request.Budget.CostUSD, 'f', -1, 64)
	}
	return fields
}

func providerResponseTraceFields(providerName, actor string, result provider.Result, providerErr error, elapsed time.Duration) map[string]string {
	fields := map[string]string{
		"actor":                 actor,
		"cost_usd":              strconv.FormatFloat(result.Usage.CostUSD, 'f', -1, 64),
		"duration_ms":           strconv.FormatInt(elapsed.Milliseconds(), 10),
		"final_message_present": strconv.FormatBool(result.FinalMessage != ""),
		"input_tokens":          strconv.FormatInt(result.Usage.InputTokens, 10),
		"output_tokens":         strconv.FormatInt(result.Usage.OutputTokens, 10),
		"provider":              providerName,
		"result":                "success",
	}
	if providerErr != nil {
		fields["result"] = "failure"
		fields["outcome"] = providerErrorOutcome(providerErr)
	}
	return fields
}

func providerErrorOutcome(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "error"
	}
}

func toolTraceFields(name string, tool workflow.Tool) map[string]string {
	return map[string]string{
		"capture_log":       strconv.FormatBool(tool.Capture.Log != ""),
		"capture_stderr":    strconv.FormatBool(tool.Capture.Stderr),
		"capture_stdout":    strconv.FormatBool(tool.Capture.Stdout),
		"mutates_workspace": strconv.FormatBool(tool.MutatesWorkspace),
		"tool":              name,
		"tool_type":         tool.Type,
	}
}

func finishToolTraceFields(fields map[string]string, result string, elapsed time.Duration, runErr error) map[string]string {
	finished := make(map[string]string, len(fields)+3)
	for key, value := range fields {
		finished[key] = value
	}
	finished["duration_ms"] = strconv.FormatInt(elapsed.Milliseconds(), 10)
	finished["result"] = result
	var exitErr execExitError
	if errors.As(runErr, &exitErr) {
		finished["exit_code"] = strconv.Itoa(exitErr.ExitCode())
	}
	return finished
}

type execExitError interface {
	ExitCode() int
}
