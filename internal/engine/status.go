package engine

import (
	"fmt"
	"io"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

// StatusSnapshot is the stable, non-secret view of durable workflow state.
// ValidationError is intentionally not included: validation output may contain
// arbitrary command output, while the validation name and failure kind are
// sufficient for machine-readable state classification.
type StatusSnapshot struct {
	SchemaVersion    int    `json:"schema_version"`
	Workflow         string `json:"workflow"`
	Repo             string `json:"repo"`
	Initialized      bool   `json:"initialized"`
	State            string `json:"state"`
	HumanGate        string `json:"human_gate,omitempty"`
	Base             string `json:"base,omitempty"`
	Branch           string `json:"branch,omitempty"`
	ActivePhase      string `json:"active_phase,omitempty"`
	PhaseStartCommit string `json:"phase_start_commit,omitempty"`
	ActorCompleted   bool   `json:"actor_completed"`
	FailureKind      string `json:"failure_kind,omitempty"`
	ValidationFailed string `json:"validation_failed,omitempty"`
	// Recovery and NextAction are stable, non-secret classifications. They
	// describe how the existing runtime will evaluate a later run; they never
	// authorize recovery or expose validation command output.
	Recovery        string `json:"recovery,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
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

	state := "uninitialized"
	if initialized {
		state = "ready"
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

	pendingGate := ""
	if initialized && !completed && !activeExists {
		if gate, err := e.pendingHumanGateForStatus(); err != nil {
			return StatusSnapshot{}, err
		} else if gate != "" {
			state = "human-gated"
			pendingGate = gate
		}
	}

	snapshot := StatusSnapshot{
		SchemaVersion:  1,
		Workflow:       e.Workflow.Metadata.Name,
		Repo:           e.Repo.Root,
		Initialized:    initialized,
		State:          state,
		HumanGate:      pendingGate,
		Base:           base,
		Branch:         branch,
		ActorCompleted: active.ActorCompleted,
		Complete:       completed,
		CompleteCommit: completeCommit,
	}
	if activeExists {
		snapshot.ActivePhase = active.PhaseID
		snapshot.PhaseStartCommit = active.StartCommit
		snapshot.ActorCompleted = active.ActorCompleted
		snapshot.FailureKind = string(active.FailureKind)
		snapshot.ValidationFailed = active.Validation
		snapshot.validationError = active.ValidationError
	}
	setRecoveryMetadata(&snapshot)
	return snapshot, nil
}

func setRecoveryMetadata(snapshot *StatusSnapshot) {
	switch snapshot.State {
	case "validation-failed/recoverable":
		snapshot.Recovery = "automatic-on-rerun"
		snapshot.NextAction = "rerun"
	case "safety-failed/terminal":
		snapshot.Recovery = "operator-action-required"
		snapshot.NextAction = "remediate-then-rerun"
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
	if snapshot.Recovery != "" {
		p.MetadataStyled("recovery", snapshot.Recovery, clioutput.StateRole(snapshot.Recovery))
		p.MetadataStyled("next_action", snapshot.NextAction, clioutput.StateRole(snapshot.NextAction))
		switch snapshot.Recovery {
		case "automatic-on-rerun":
			p.Line(clioutput.RoleWarning, "recovery guidance: correct the validation failure if needed, then rerun; durable phase state will be used. Use reset only to intentionally abandon the run")
		case "operator-action-required":
			p.Line(clioutput.RoleError, "recovery guidance: operator action is required; automatic actor and repair execution stopped. repair or revert the workspace-policy violation, then rerun. reset is not required merely because the safety failure occurred")
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
		return "AgentFlow recovery: operator action is required. Automatic actor and repair recovery stopped for the current workspace-policy violation. " +
			"Repair or revert the violation, inspect status and logs, then rerun the same agentflow run command (the same invocation) so normal durable recovery checks can decide whether to resume. " +
			"Reset is not required merely because the safety failure occurred; use reset only to intentionally abandon this durable run."
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
