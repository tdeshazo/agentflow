package engine

import (
	"fmt"
	"io"

	"github.com/tdeshazo/agentflow-spec/internal/clioutput"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
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
	Complete         bool   `json:"complete"`
	CompleteCommit   string `json:"complete_commit,omitempty"`
	validationError  string
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
	return snapshot, nil
}

// Status writes the human-readable status form. Redirected and buffered output
// keeps the historical plain-text bytes; terminal output may add ANSI styling.
func (e *Engine) Status() error {
	return e.StatusTo(e.Out, clioutput.IsTTY(e.Out), clioutput.ColorEnabled(e.Out))
}

// StatusTo writes human-readable status using an explicit presentation mode.
// It exists so TTY/color branches can be tested without a real terminal.
func (e *Engine) StatusTo(out io.Writer, tty, color bool) error {
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return err
	}
	p := clioutput.NewPresenterWithMode(out, tty, color)
	label := p.Label

	fmt.Fprintf(out, "%s %s\n", label("workflow"), snapshot.Workflow)
	fmt.Fprintf(out, "%s %s\n", label("repo"), snapshot.Repo)
	fmt.Fprintf(out, "%s %v\n", label("initialized"), snapshot.Initialized)
	fmt.Fprintf(out, "%s %s\n", label("state"), p.State(snapshot.State))
	if snapshot.HumanGate != "" {
		fmt.Fprintf(out, "%s %s\n", label("human_gate"), p.Style(clioutput.RoleWarning, snapshot.HumanGate))
	}
	if snapshot.Initialized {
		fmt.Fprintf(out, "%s %s\n%s %s\n", label("base"), snapshot.Base, label("branch"), snapshot.Branch)
	}
	if snapshot.ActivePhase != "" {
		fmt.Fprintf(out, "%s %s @ %s\n", label("active_phase"), p.Style(clioutput.RoleAccent, snapshot.ActivePhase), snapshot.PhaseStartCommit)
		fmt.Fprintf(out, "%s %v\n", label("actor_completed"), snapshot.ActorCompleted)
		if snapshot.ValidationFailed != "" {
			if snapshot.FailureKind != "" {
				fmt.Fprintf(out, "%s %s\n", label("failure_kind"), p.State(snapshot.FailureKind))
			}
			fmt.Fprintf(out, "%s %s\n", label("validation_failed"), p.Style(clioutput.RoleWarning, snapshot.ValidationFailed))
			// Preserve the existing diagnostic in text output. It is deliberately
			// absent from StatusSnapshot because it may contain command output.
			fmt.Fprintf(out, "%s %s\n", label("validation_error"), p.Style(clioutput.RoleError, snapshot.validationError))
		}
	}
	completeRole := clioutput.RoleMuted
	if snapshot.Complete {
		completeRole = clioutput.RoleSuccess
	}
	fmt.Fprintf(out, "%s %s", label("complete"), p.Style(completeRole, fmt.Sprint(snapshot.Complete)))
	if snapshot.Complete {
		fmt.Fprintf(out, " @ %s", snapshot.CompleteCommit)
	}
	fmt.Fprintln(out)
	return nil
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
