package engine

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/executiontrace"
	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

// StatusSnapshot is the stable, non-secret view of durable workflow state.
// LastError is bounded and redacts environment-shaped diagnostics before it is
// persisted; it remains diagnostic only and never authorizes recovery.
type StatusSnapshot struct {
	SchemaVersion      int      `json:"schema_version"`
	RunID              string   `json:"run_id,omitempty"`
	TraceSchemaVersion int      `json:"trace_schema_version,omitempty"`
	TracePath          string   `json:"trace_path,omitempty"`
	Workflow           string   `json:"workflow"`
	Repo               string   `json:"repo"`
	Initialized        bool     `json:"initialized"`
	State              string   `json:"state"`
	HumanGate          string   `json:"human_gate,omitempty"`
	Base               string   `json:"base,omitempty"`
	Branch             string   `json:"branch,omitempty"`
	ActivePhase        string   `json:"active_phase,omitempty"`
	NodeExecutionID    string   `json:"node_execution_id,omitempty"`
	NodeAttempt        int      `json:"node_attempt,omitempty"`
	ParallelPhases     []string `json:"parallel_phases,omitempty"`
	PhaseStartCommit   string   `json:"phase_start_commit,omitempty"`
	ActorCompleted     bool     `json:"actor_completed"`
	FailureKind        string   `json:"failure_kind,omitempty"`
	ValidationFailed   string   `json:"validation_failed,omitempty"`
	FailureStage       string   `json:"failure_stage,omitempty"`
	LastError          string   `json:"last_error,omitempty"`
	QuarantinePath     string   `json:"quarantine_path,omitempty"`
	BudgetStartedAt    string   `json:"budget_started_at,omitempty"`
	ModelCalls         int      `json:"model_calls,omitempty"`
	ToolCalls          int      `json:"tool_calls,omitempty"`
	Tokens             int64    `json:"tokens,omitempty"`
	CostUSD            float64  `json:"cost_usd,omitempty"`
	BudgetExhausted    string   `json:"budget_exhausted,omitempty"`
	// Recovery and NextAction are stable, non-secret classifications. They
	// describe how the existing runtime will evaluate a later run; they never
	// authorize recovery or expose validation command output.
	Recovery   string `json:"recovery,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	*gitstate.IntegrityViolation
	Complete        bool   `json:"complete"`
	CompleteCommit  string `json:"complete_commit,omitempty"`
	validationError string
}

func (e *Engine) statusSnapshot() (StatusSnapshot, error) {
	base, initialized, err := e.Store.Resolve(e.baseRecord())
	if err != nil {
		return StatusSnapshot{}, err
	}
	completed, completeCommit, err := e.validCommitMarker(e.workflowCompleteMarker())
	if err != nil {
		return StatusSnapshot{}, err
	}
	var branch string
	if _, err := e.Store.GetJSON(e.branchRecord(), &branch); err != nil {
		return StatusSnapshot{}, err
	}
	var active ActivePhase
	activeExists, err := e.Store.GetJSON(e.activeRecord(), &active)
	if err != nil {
		return StatusSnapshot{}, err
	}
	if activeExists {
		if err := gitstate.ValidateQuarantinePath(e.Repo, active.QuarantinePath); err != nil {
			return StatusSnapshot{}, fmt.Errorf("active phase quarantine diagnostic: %w", err)
		}
		if err := active.IntegrityViolation.Validate(); err != nil {
			return StatusSnapshot{}, fmt.Errorf("active phase integrity diagnostic: %w", err)
		}
		if active.PhaseID == "" {
			return StatusSnapshot{}, fmt.Errorf("active phase record %q has no phase id", e.activeRecord())
		}
		if active.StartCommit == "" {
			return StatusSnapshot{}, fmt.Errorf("active phase record %q has no start commit", e.activeRecord())
		}
		if !e.Repo.ObjectExists(active.StartCommit + "^{commit}") {
			return StatusSnapshot{}, fmt.Errorf("active phase record %q has an invalid start commit", e.activeRecord())
		}
	}
	var batch parallelBatch
	batchExists, err := e.Store.GetJSON(parallelBatchRecord, &batch)
	if err != nil {
		return StatusSnapshot{}, err
	}
	if batchExists {
		if err := e.validateParallelBatch(batch); err != nil {
			return StatusSnapshot{}, fmt.Errorf("parallel scheduler status: %w", err)
		}
	}
	var lastFailure gitstate.FailureRecord
	failureExists, err := e.Store.GetJSON(e.lastFailureRecord(), &lastFailure)
	if err != nil {
		return StatusSnapshot{}, err
	}
	if err := lastFailure.IntegrityViolation.Validate(); err != nil {
		return StatusSnapshot{}, fmt.Errorf("last failure integrity diagnostic: %w", err)
	}
	if err := gitstate.ValidateQuarantinePath(e.Repo, lastFailure.QuarantinePath); err != nil {
		return StatusSnapshot{}, fmt.Errorf("last failure quarantine diagnostic: %w", err)
	}
	var resourceUsage ResourceUsage
	usageExists, err := e.Store.GetJSON(resourceUsageRecord, &resourceUsage)
	if err != nil {
		return StatusSnapshot{}, err
	}
	if usageExists && resourceUsage.Version != 1 {
		return StatusSnapshot{}, fmt.Errorf("unsupported resource usage version %d", resourceUsage.Version)
	}
	var runIdentity RunIdentity
	identityExists, err := e.Store.GetJSON(e.runIdentityRecord(), &runIdentity)
	if err != nil {
		return StatusSnapshot{}, err
	}
	tracePath := ""
	if identityExists && runIdentity.RunID != "" {
		tracePath, err = executiontrace.Path(e.Repo, runIdentity.RunID)
		if err != nil {
			return StatusSnapshot{}, err
		}
	}

	state := "uninitialized"
	if initialized {
		state = "ready"
	}
	if !initialized && activeExists {
		// Match repository-wide status: an active record without the base
		// initialization record is not resumable state and must not be presented
		// as a fresh, uninitialized workflow.
		state = "stale"
	}
	if completed {
		state = "completed"
	}
	if initialized && !completed && activeExists {
		state = "active"
		switch active.FailureKind {
		case PhaseFailureSafety:
			state = "safety-failed/terminal"
		case PhaseFailureValidation:
			state = "validation-failed/recoverable"
		case "":
			// Active records written by older runtimes did not classify the
			// failure. Preserve their previous status presentation while the
			// next lifecycle attempt records a typed classification.
			if active.Validation != "" {
				state = "validation-failed/recoverable"
			}
		}
	}
	if initialized && !completed && !activeExists && batchExists {
		state = "parallel-active"
	}
	if initialized && !completed && !activeExists && failureExists {
		state = "failed/retryable"
	}
	if initialized && !completed && resourceUsage.Exhausted != "" {
		state = "budget-exhausted/terminal"
	}

	pendingGate := ""
	if initialized && !completed && !activeExists && !batchExists {
		if gate, err := e.pendingHumanGateForStatus(); err != nil {
			return StatusSnapshot{}, err
		} else if gate != "" {
			state = "human-gated"
			pendingGate = gate
		}
	}

	snapshot := StatusSnapshot{
		SchemaVersion:      1,
		RunID:              runIdentity.RunID,
		TraceSchemaVersion: executiontrace.SchemaVersion,
		TracePath:          tracePath,
		Workflow:           e.Workflow.Metadata.Name,
		Repo:               e.Repo.Root,
		Initialized:        initialized,
		State:              state,
		HumanGate:          pendingGate,
		Base:               base,
		Branch:             branch,
		ActorCompleted:     active.ActorCompleted,
		Complete:           completed,
		CompleteCommit:     completeCommit,
		FailureStage:       lastFailure.Stage,
		LastError:          lastFailure.Error,
		QuarantinePath:     lastFailure.QuarantinePath,
		IntegrityViolation: lastFailure.IntegrityViolation,
		ParallelPhases:     append([]string(nil), batch.Phases...),
		ModelCalls:         resourceUsage.ModelCalls,
		ToolCalls:          resourceUsage.ToolCalls,
		Tokens:             resourceUsage.Tokens,
		CostUSD:            resourceUsage.CostUSD,
		BudgetExhausted:    resourceUsage.Exhausted,
	}
	if !resourceUsage.StartedAt.IsZero() {
		snapshot.BudgetStartedAt = resourceUsage.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if activeExists {
		snapshot.ActivePhase = active.PhaseID
		snapshot.NodeExecutionID = active.NodeExecutionID
		snapshot.NodeAttempt = active.Attempt
		snapshot.PhaseStartCommit = active.StartCommit
		snapshot.ActorCompleted = active.ActorCompleted
		snapshot.FailureKind = string(active.FailureKind)
		snapshot.ValidationFailed = active.Validation
		snapshot.QuarantinePath = active.QuarantinePath
		snapshot.validationError = active.ValidationError
		snapshot.IntegrityViolation = active.IntegrityViolation
	}
	setRecoveryMetadata(&snapshot)
	return snapshot, nil
}

func setRecoveryMetadata(snapshot *StatusSnapshot) {
	switch snapshot.State {
	case "parallel-active":
		snapshot.Recovery = "automatic-on-rerun"
		snapshot.NextAction = "rerun"
	case "validation-failed/recoverable":
		snapshot.Recovery = "automatic-on-rerun"
		snapshot.NextAction = "rerun"
	case "safety-failed/terminal":
		snapshot.Recovery = "operator-action-required"
		snapshot.NextAction = "reset-or-abandon"
	case "budget-exhausted/terminal":
		snapshot.Recovery = "operator-action-required"
		snapshot.NextAction = "reset-or-abandon"
	case "failed/retryable":
		snapshot.Recovery = "automatic-on-rerun"
		snapshot.NextAction = "rerun"
	}
}

// Status writes the human-readable status form. Redirected and buffered output
// keeps the historical plain-text bytes; terminal output may add ANSI styling.
func (e *Engine) Status() error {
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return err
	}
	p := clioutput.NewPresenter(e.Out)
	return writeStatusSnapshot(p, snapshot)
}

// StatusTo writes human-readable status using an explicit presentation mode.
// It exists so TTY/color branches can be tested without a real terminal.
func (e *Engine) StatusTo(out io.Writer, tty, color bool) error {
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return err
	}
	p := clioutput.NewPresenterWithMode(out, tty, color)
	return writeStatusSnapshot(p, snapshot)
}

func writeStatusSnapshot(p clioutput.Presenter, snapshot StatusSnapshot) error {
	p.Metadata("workflow", snapshot.Workflow)
	p.Metadata("repo", p.Hyperlink(snapshot.Repo, clioutput.FileURL(snapshot.Repo)))
	p.Metadata("initialized", fmt.Sprint(snapshot.Initialized))
	p.MetadataStyled("state", snapshot.State, clioutput.StateRole(snapshot.State))
	if snapshot.HumanGate != "" {
		p.MetadataStyled("human_gate", snapshot.HumanGate, clioutput.RoleWarning)
	}
	if snapshot.Initialized {
		p.Metadata("run_id", snapshot.RunID)
		p.Metadata("trace", p.Hyperlink(snapshot.TracePath, clioutput.FileURL(snapshot.TracePath)))
		p.Metadata("base", snapshot.Base)
		p.Metadata("branch", snapshot.Branch)
	}
	if snapshot.ActivePhase != "" {
		p.MetadataStyled(
			"active_phase",
			fmt.Sprintf("%s @ %s", snapshot.ActivePhase, snapshot.PhaseStartCommit),
			clioutput.RoleAccent,
		)
		p.Metadata("actor_completed", fmt.Sprint(snapshot.ActorCompleted))
		if snapshot.NodeExecutionID != "" {
			p.Metadata("node_execution_id", snapshot.NodeExecutionID)
			p.Metadata("node_attempt", fmt.Sprint(snapshot.NodeAttempt))
		}
		if snapshot.ValidationFailed != "" {
			if snapshot.FailureKind != "" {
				p.MetadataStyled("failure_kind", snapshot.FailureKind, clioutput.StateRole(snapshot.FailureKind))
			}
			p.MetadataStyled("validation_failed", snapshot.ValidationFailed, clioutput.RoleWarning)
			// Preserve the existing diagnostic in text output. It is deliberately
			// absent from StatusSnapshot because it may contain command output.
			p.MetadataStyled("validation_error", snapshot.validationError, clioutput.RoleError)
		}
	}
	if len(snapshot.ParallelPhases) != 0 {
		p.MetadataStyled("parallel_phases", strings.Join(snapshot.ParallelPhases, ", "), clioutput.RoleAccent)
	}
	if snapshot.BudgetStartedAt != "" {
		p.Metadata("budget_started_at", snapshot.BudgetStartedAt)
		p.Metadata("model_calls", fmt.Sprint(snapshot.ModelCalls))
		p.Metadata("tool_calls", fmt.Sprint(snapshot.ToolCalls))
		p.Metadata("tokens", fmt.Sprint(snapshot.Tokens))
		if snapshot.CostUSD != 0 {
			p.Metadata("cost_usd", fmt.Sprintf("%.6f", snapshot.CostUSD))
		}
	}
	if snapshot.BudgetExhausted != "" {
		p.MetadataStyled("budget_exhausted", snapshot.BudgetExhausted, clioutput.RoleError)
	}
	if snapshot.FailureStage != "" {
		p.MetadataStyled("failure_stage", snapshot.FailureStage, clioutput.RoleWarning)
		p.MetadataStyled("last_error", snapshot.LastError, clioutput.RoleError)
	}
	if snapshot.QuarantinePath != "" {
		p.Metadata("quarantine", p.Hyperlink(snapshot.QuarantinePath, clioutput.FileURL(snapshot.QuarantinePath)))
	}
	writeIntegrityViolation(p, "", snapshot.IntegrityViolation)
	if snapshot.Recovery != "" {
		p.MetadataStyled("recovery", snapshot.Recovery, clioutput.StateRole(snapshot.Recovery))
		p.MetadataStyled("next_action", snapshot.NextAction, clioutput.StateRole(snapshot.NextAction))
		switch snapshot.Recovery {
		case "automatic-on-rerun":
			p.Line(clioutput.RoleWarning, "recovery guidance: correct the validation failure if needed, then rerun; durable phase state will be used. Use reset only to intentionally abandon the run")
		case "operator-action-required":
			p.Line(clioutput.RoleError, "recovery guidance: operator action is required; terminal safety prevents this durable run from continuing. reset the run to start again, or abandon it")
		}
	}
	completeRole := clioutput.RoleMuted
	if snapshot.Complete {
		completeRole = clioutput.RoleSuccess
	}
	completeValue := fmt.Sprint(snapshot.Complete)
	if snapshot.Complete {
		completeValue += " @ " + snapshot.CompleteCommit
	}
	p.MetadataStyled("complete", completeValue, completeRole)
	return nil
}

func writeIntegrityViolation(p clioutput.Presenter, indent string, violation *gitstate.IntegrityViolation) {
	if violation == nil {
		return
	}
	p.IndentedMetadata(indent, "integrity_rule", violation.IntegrityRule, clioutput.RoleError)
	writeIntegrityPaths(p, indent, "changed", violation.Changed)
	writeIntegrityPaths(p, indent, "added", violation.Added)
	writeIntegrityPaths(p, indent, "removed", violation.Removed)
}

func writeIntegrityPaths(p clioutput.Presenter, indent, label string, paths []string) {
	if len(paths) == 0 {
		p.IndentedMetadata(indent, label, "[]", clioutput.RolePlain)
		return
	}
	p.Line(clioutput.RolePlain, "%s%s:", indent, label)
	for _, path := range paths {
		p.Line(clioutput.RolePlain, "%s  - %s", indent, path)
	}
}

// FailureRecoveryGuidance reports durable recovery advice for a failed run.
// An empty result means durable state cannot safely support recovery advice.
func (e *Engine) FailureRecoveryGuidance() string {
	if !e.recoveryEligible {
		return ""
	}
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return ""
	}

	switch snapshot.State {
	case "active":
		if snapshot.ActivePhase == "" {
			return ""
		}
		return "AgentFlow recovery: retained phase work and durable state are preserved. " +
			"Inspect status and logs, then rerun the same agentflow run command (the same invocation); AgentFlow will resume from durable phase state rather than discard accepted work. " +
			"Use reset only to intentionally abandon this durable run."
	case "validation-failed/recoverable":
		return "AgentFlow recovery: retained phase work is preserved in durable state. " +
			"Inspect status and logs, then rerun the same agentflow run command (the same invocation); rerunning is the normal recovery action. AgentFlow will resume from durable phase state rather than discard accepted work. " +
			"Use reset only to intentionally abandon this durable run."
	case "safety-failed/terminal":
		return "AgentFlow recovery: operator action is required. Terminal safety prevents this durable run from continuing. " +
			"Reset the run to start again, or abandon it."
	default:
		return ""
	}
}

// StatusJSON writes one JSON object describing the durable workflow state.
func (e *Engine) StatusJSON() error {
	return e.StatusJSONTo(e.Out, clioutput.IsTTY(e.Out))
}

// StatusJSONTo writes one JSON object describing the durable workflow state
// to out using the supplied terminal presentation policy.
func (e *Engine) StatusJSONTo(out io.Writer, tty bool) error {
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return err
	}
	return clioutput.WriteJSONWithTTY(out, snapshot, tty)
}

func (e *Engine) pendingHumanGateForStatus() (string, error) {
	for _, gate := range e.Workflow.Spec.HumanGates {
		condition := gate.When
		if gate.If != "" {
			condition = gate.If
		}
		if !e.statusExpressionAvailable(condition) {
			continue
		}
		required, err := e.bool(nil, condition)
		if err != nil {
			return "", err
		}
		if !required {
			continue
		}
		record := "human/" + gate.ID
		if gate.IdempotentRecord != "" {
			if !e.statusExpressionAvailable(gate.IdempotentRecord) {
				continue
			}
			resolved, err := e.recordName(gate.IdempotentRecord, nil)
			if err != nil {
				return "", err
			}
			record = resolved
		}
		if ok, _, err := e.validCommitMarker(record); err != nil {
			return "", err
		} else if ok {
			continue
		}
		ready := true
		for _, prerequisite := range gate.Requires {
			phase, err := e.phaseByID(prerequisite)
			if err != nil {
				return "", err
			}
			ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase))
			if err != nil {
				return "", err
			}
			if !ok {
				ready = false
				break
			}
		}
		if ready {
			return gate.ID, nil
		}
	}
	return "", nil
}

func (e *Engine) statusExpressionAvailable(expression string) bool {
	if e.parametersResolved {
		return true
	}
	parameters, err := workflow.ParameterReferences(expression)
	if err != nil || len(parameters) != 0 {
		return false
	}
	environment, err := workflow.ExpressionEnvironmentReferences(expression)
	return err == nil && len(environment) == 0
}
