package engine

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/tdeshazo/agentflow/internal/executiontrace"
)

// ExplainReport is a bounded, non-secret explanation of one workflow phase.
// Durable Git state decides the classification. Trace events add only the
// most recent diagnostic reason for a recorded skip or failed attempt.
type ExplainReport struct {
	SchemaVersion   int      `json:"schema_version"`
	Workflow        string   `json:"workflow"`
	Node            string   `json:"node"`
	RunID           string   `json:"run_id,omitempty"`
	State           string   `json:"state"`
	Reason          string   `json:"reason"`
	Source          string   `json:"source"`
	WaitingOn       []string `json:"waiting_on,omitempty"`
	NodeExecutionID string   `json:"node_execution_id,omitempty"`
	Attempt         int      `json:"attempt,omitempty"`
	FailureKind     string   `json:"failure_kind,omitempty"`
	FailureStage    string   `json:"failure_stage,omitempty"`
	Actor           string   `json:"actor,omitempty"`
	Provider        string   `json:"provider,omitempty"`
}

const explainTraceEventLimit = 200

type skippedNodeEvidence struct {
	Version int    `json:"version"`
	Reason  string `json:"reason"`
}

// Explain reports why node is blocked, skipped, or failed without replaying
// work. It never renders durable validation output, provider output, prompts,
// or private model reasoning.
func (e *Engine) Explain(node string) (ExplainReport, error) {
	var identity RunIdentity
	identityOK, err := e.Store.GetJSON(e.runIdentityRecord(), &identity)
	if err != nil {
		return ExplainReport{}, err
	}
	if identityOK {
		digest, digestErr := e.expectedWorkflowDigest()
		if digestErr != nil {
			return ExplainReport{}, digestErr
		}
		if identity.Algorithm != "sha256" || identity.WorkflowDigest != digest {
			return ExplainReport{}, fmt.Errorf("cannot explain workflow state: executable workflow definition changed; use the definition that created the run or reset workflow state")
		}
	}
	phase, err := e.phaseByID(node)
	if err != nil {
		return ExplainReport{}, err
	}
	snapshot, err := e.statusSnapshot()
	if err != nil {
		return ExplainReport{}, err
	}
	report := ExplainReport{
		SchemaVersion: 1,
		Workflow:      e.Workflow.Metadata.Name,
		Node:          phase.ID,
		RunID:         snapshot.RunID,
		State:         "blocked",
		Reason:        "node has not reached its deterministic acceptance boundary",
		Source:        "git-state",
	}

	if snapshot.ActivePhase == phase.ID {
		report.NodeExecutionID = snapshot.NodeExecutionID
		report.Attempt = snapshot.NodeAttempt
		var active ActivePhase
		if ok, _ := e.Store.GetJSON(e.activeRecord(), &active); ok && active.PhaseID == phase.ID {
			report.FailureKind = string(active.FailureKind)
			report.FailureStage = active.FailureStage
			report.Actor = active.FailureActor
			report.Provider = active.FailureProvider
		}
		switch snapshot.FailureKind {
		case string(PhaseFailureSafety):
			report.State = "failed"
			report.Reason = "node is terminally blocked by a workspace safety policy"
		case string(PhaseFailureValidation):
			report.State = "failed"
			report.Reason = "node failed deterministic validation"
		case string(PhaseFailureProvider):
			report.State = "failed"
			report.Reason = "node provider invocation failed"
		default:
			report.Reason = "node has an active durable attempt"
		}
		return report, nil
	}

	if accepted, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil {
		return ExplainReport{}, err
	} else if accepted {
		var skipped skippedNodeEvidence
		if ok, err := e.Store.GetJSON(e.skippedNodeRecord(phase.ID), &skipped); err != nil {
			return ExplainReport{}, err
		} else if ok {
			if skipped.Version != 1 || safeExplainReason(skipped.Reason, "") == "" {
				return ExplainReport{}, fmt.Errorf("node %s has malformed durable skip explanation", phase.ID)
			}
			report.State = "skipped"
			report.Reason = skipped.Reason
			return report, nil
		}
		if event, ok := e.explainTraceEvent(snapshot.RunID, phase.ID, "node_skipped"); ok {
			report.State = "skipped"
			report.Source = "git-state+trace"
			report.Reason = safeExplainReason(event.Fields["reason"], "node condition evaluated false")
			report.NodeExecutionID = event.NodeExecutionID
			report.Attempt = event.Attempt
			return report, nil
		}
		report.State = "accepted"
		report.Reason = "node has durable deterministic acceptance evidence"
		return report, nil
	}

	if snapshot.FailureStage == "phase/"+phase.ID {
		report.State = "failed"
		report.Reason = "node attempt ended before a durable active-phase record could be retained"
		if event, ok := e.explainTraceEvent(snapshot.RunID, phase.ID, "node_attempt_blocked"); ok {
			report.Source = "git-state+trace"
			report.NodeExecutionID = event.NodeExecutionID
			report.Attempt = event.Attempt
		}
		return report, nil
	}

	for _, dependency := range e.Workflow.DependencyGraph.Dependencies(phase.ID) {
		dependencyPhase, err := e.phaseByID(dependency)
		if err != nil {
			return ExplainReport{}, fmt.Errorf("node %s dependency %q: %w", phase.ID, dependency, err)
		}
		accepted, _, err := e.validCommitMarker(e.phaseMarkerName(dependencyPhase))
		if err != nil {
			return ExplainReport{}, err
		}
		if !accepted {
			report.WaitingOn = append(report.WaitingOn, dependency)
		}
	}
	if len(report.WaitingOn) != 0 {
		report.Reason = "node is waiting for declared dependencies to be deterministically accepted"
	}
	return report, nil
}

func (e *Engine) skippedNodeRecord(node string) string {
	return "runtime/skipped-nodes/" + hex.EncodeToString([]byte(node))
}

func (e *Engine) explainTraceEvent(runID, node, kind string) (executiontrace.Event, bool) {
	if runID == "" {
		return executiontrace.Event{}, false
	}
	recent, err := executiontrace.ReadRecent(e.Repo, runID, explainTraceEventLimit)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return executiontrace.Event{}, false
	}
	for index := len(recent.Events) - 1; index >= 0; index-- {
		event := recent.Events[index]
		if event.NodeID == node && event.Event == kind {
			return event, true
		}
	}
	return executiontrace.Event{}, false
}

func safeExplainReason(reason, fallback string) string {
	// Trace reasons are an explicit small vocabulary produced by the runtime;
	// do not expose arbitrary diagnostic fields if an older or malformed trace
	// is encountered.
	switch reason {
	case "condition is false", "typed evidence condition is false":
		return reason
	default:
		return fallback
	}
}
