package engine

import (
	"fmt"
	"strings"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

const runLeaseRecord = "owner"

// RunLease is the runtime-owned, durable exclusive-owner record. Process
// identity includes the kernel start token, so a reused PID cannot inherit an
// abandoned lease.
type RunLease struct {
	Version int                       `json:"version"`
	Process *gitstate.ProcessMetadata `json:"process"`
}

func (e *Engine) acquireRunLease() (string, error) {
	if e.Workflow != nil {
		if record := conflictingRunLeaseRecord(e.Workflow.Spec.State.Records); record != "" {
			return "", fmt.Errorf("workflow state record %q conflicts with reserved runtime owner record", record)
		}
	}
	current := gitstate.CurrentProcessMetadata()
	if current == nil {
		return "", fmt.Errorf("cannot establish exclusive workflow owner: stable process identity is unavailable")
	}
	lease := RunLease{Version: 1, Process: current}
	for attempts := 0; attempts < 8; attempts++ {
		sha, exists, err := e.Store.Resolve(runLeaseRecord)
		if err != nil {
			return "", fmt.Errorf("read workflow owner lease: %w", err)
		}
		if !exists {
			claimed, err := e.Store.SetJSONIf(runLeaseRecord, lease, "")
			if err != nil {
				return "", fmt.Errorf("claim workflow owner lease: %w", err)
			}
			if claimed {
				owned, _, err := e.Store.Resolve(runLeaseRecord)
				return owned, err
			}
			continue
		}
		var previous RunLease
		ok, err := e.Store.GetJSON(runLeaseRecord, &previous)
		if err != nil || !ok || previous.Version != 1 || previous.Process == nil {
			return "", fmt.Errorf("workflow owner lease is malformed; refusing unsafe recovery")
		}
		liveness, verified := gitstate.ProcessLiveness(previous.Process)
		if !verified {
			return "", fmt.Errorf("cannot verify existing workflow owner PID %d; refusing unsafe recovery", previous.Process.PID)
		}
		if liveness == "running" {
			return "", fmt.Errorf("workflow is already owned by live process %d", previous.Process.PID)
		}
		claimed, err := e.Store.SetJSONIf(runLeaseRecord, lease, sha)
		if err != nil {
			return "", fmt.Errorf("recover stale workflow owner lease: %w", err)
		}
		if claimed {
			owned, _, err := e.Store.Resolve(runLeaseRecord)
			return owned, err
		}
	}
	return "", fmt.Errorf("could not establish exclusive workflow owner due to concurrent lease updates")
}

func conflictingRunLeaseRecord(records workflow.StateRecords) string {
	configured := []string{
		records.BaseCommit,
		records.Branch,
		records.ActivePhase,
		records.CompletedPhasePattern,
		records.CompletedPhases,
		records.ManualConfirmation,
		records.HumanVerification,
		records.WorkflowComplete,
	}
	for _, record := range configured {
		if strings.TrimPrefix(record, "/") == runLeaseRecord {
			return record
		}
	}
	for _, record := range records.Integrity {
		if strings.TrimPrefix(record, "/") == runLeaseRecord {
			return record
		}
	}
	return ""
}

func (e *Engine) releaseRunLease(sha string) {
	_, _ = e.Store.DeleteIf(runLeaseRecord, sha)
}
