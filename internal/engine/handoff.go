package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

const acceptedHandoffRecordVersion = 1

// AcceptedHandoff is durable only after the same phase has passed its
// deterministic lifecycle and published the commit marker.
type AcceptedHandoff struct {
	Version         int             `json:"version"`
	RunID           string          `json:"run_id"`
	NodeExecutionID string          `json:"node_execution_id"`
	PhaseID         string          `json:"phase_id"`
	AcceptedCommit  string          `json:"accepted_commit"`
	Digest          string          `json:"digest"`
	Payload         semanticHandoff `json:"payload"`
}

func (e *Engine) stagedHandoffRecord(phaseID string) string   { return "handoffs/staged/" + phaseID }
func (e *Engine) acceptedHandoffRecord(phaseID string) string { return "handoffs/accepted/" + phaseID }

func phaseRequiresStructuredHandoff(p *workflow.Phase) bool {
	return p != nil && (p.Kind == "audit" || len(p.Outputs) != 0)
}

func providerSupportsStructuredHandoff(p provider.Provider) bool {
	contract, ok := provider.ContractFor(p)
	return ok && contract.Version == provider.ContractVersionV2 && containsContextVersion(contract.InvocationContextVersions, provider.InvocationContextVersionV2) && containsHandoffVersion(contract.HandoffVersions, provider.HandoffVersionV1)
}
func handoffRequest(required bool) *provider.HandoffRequest {
	if !required {
		return nil
	}
	return &provider.HandoffRequest{Version: provider.HandoffVersionV1}
}
func containsHandoffVersion(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func containsContextVersion(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (e *Engine) stageHandoff(p *workflow.Phase, raw []byte, credentials map[string]string) error {
	if !phaseRequiresStructuredHandoff(p) {
		return nil
	}
	credentialValues := make([]string, 0, len(credentials))
	for _, value := range credentials {
		credentialValues = append(credentialValues, value)
	}
	parsed, err := provider.ParseHandoffWithCredentials(raw, credentialValues)
	if err != nil {
		return err
	}
	if parsed.Status == "blocked" {
		return fmt.Errorf("provider reported blocked structured handoff")
	}
	if e.runID == "" || !validStableID(e.nodeExecutionID, "node") {
		return fmt.Errorf("structured handoff has no bound run and node identity")
	}
	handoff := semanticHandoffFromProvider(parsed)
	if err := handoff.Validate(); err != nil {
		return err
	}
	canonical, _ := json.Marshal(handoff)
	digest := sha256.Sum256(canonical)
	record := AcceptedHandoff{Version: acceptedHandoffRecordVersion, RunID: e.runID, NodeExecutionID: e.nodeExecutionID, PhaseID: p.ID, Digest: "sha256:" + hex.EncodeToString(digest[:]), Payload: handoff}
	if err := e.Store.SetJSON(e.stagedHandoffRecord(p.ID), record); err != nil {
		return err
	}
	e.traceEvent("handoff_staged", map[string]string{"phase": p.ID, "digest": record.Digest, "status": handoff.Status})
	return nil
}

func (e *Engine) publishAcceptedHandoff(p *workflow.Phase, commit string) error {
	if !phaseRequiresStructuredHandoff(p) {
		return nil
	}
	// A legacy provider is intentionally a v1 compatibility path. A negotiated
	// v2 adapter, on the other hand, cannot advance an audit/output phase
	// without its requested handoff.
	agent, known := e.Workflow.Spec.Agents[p.Actor]
	if !known || !providerSupportsStructuredHandoff(e.Providers[agent.Runner]) {
		return nil
	}
	var staged AcceptedHandoff
	ok, err := e.Store.GetJSON(e.stagedHandoffRecord(p.ID), &staged)
	if err != nil {
		return err
	}
	if !ok || staged.Version != acceptedHandoffRecordVersion || staged.RunID != e.runID || staged.NodeExecutionID != e.nodeExecutionID || staged.PhaseID != p.ID || staged.Digest == "" {
		return fmt.Errorf("phase %s requires a compatible structured handoff before acceptance", p.ID)
	}
	if err := staged.Payload.Validate(); err != nil {
		return fmt.Errorf("phase %s handoff: %w", p.ID, err)
	}
	canonical, _ := json.Marshal(staged.Payload)
	digest := sha256.Sum256(canonical)
	if staged.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return fmt.Errorf("phase %s staged handoff digest does not match payload", p.ID)
	}
	staged.AcceptedCommit = commit
	if err := e.Store.SetJSON(e.acceptedHandoffRecord(p.ID), staged); err != nil {
		return fmt.Errorf("persist accepted handoff for phase %s: %w", p.ID, err)
	}
	e.traceEvent("handoff_accepted", map[string]string{"phase": p.ID, "commit": commit, "digest": staged.Digest})
	return nil
}

func (e *Engine) compileAcceptedHandoffs(consumer *workflow.Phase) ([]semanticHandoffReference, error) {
	result := []semanticHandoffReference{}
	if consumer == nil {
		return result, nil
	}
	for _, dependencyID := range e.Workflow.DependencyGraph.Dependencies(consumer.ID) {
		dependency, err := e.phaseByID(dependencyID)
		if err != nil {
			return nil, err
		}
		commit, accepted, err := e.Store.Resolve(e.phaseMarkerName(dependency))
		if err != nil {
			return nil, err
		}
		if !accepted {
			continue
		}
		var handoff AcceptedHandoff
		ok, err := e.Store.GetJSON(e.acceptedHandoffRecord(dependencyID), &handoff)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		} // v1 and ordinary phases deliberately have no handoff.
		if handoff.Version != acceptedHandoffRecordVersion || handoff.RunID != e.runID || !validStableID(handoff.NodeExecutionID, "node") || handoff.PhaseID != dependencyID || handoff.AcceptedCommit != commit || handoff.Digest == "" {
			return nil, fmt.Errorf("dependency %s has malformed or stale accepted handoff", dependencyID)
		}
		if err := handoff.Payload.Validate(); err != nil {
			return nil, fmt.Errorf("dependency %s accepted handoff: %w", dependencyID, err)
		}
		canonical, _ := json.Marshal(handoff.Payload)
		digest := sha256.Sum256(canonical)
		if handoff.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
			return nil, fmt.Errorf("dependency %s has malformed or stale accepted handoff", dependencyID)
		}
		result = append(result, semanticHandoffReference{Producer: dependencyID, Commit: commit, Digest: handoff.Digest, Payload: handoff.Payload})
	}
	return result, nil
}

func (e *Engine) validateRecoveredAcceptedHandoff(p *workflow.Phase, commit, nodeExecutionID string) error {
	if !phaseRequiresStructuredHandoff(p) {
		return nil
	}
	var handoff AcceptedHandoff
	ok, err := e.Store.GetJSON(e.acceptedHandoffRecord(p.ID), &handoff)
	if err != nil {
		return err
	}
	if !ok || handoff.Version != acceptedHandoffRecordVersion || handoff.RunID != e.runID || handoff.NodeExecutionID != nodeExecutionID || handoff.PhaseID != p.ID || handoff.AcceptedCommit != commit || handoff.Digest == "" {
		return fmt.Errorf("phase %s has malformed or stale accepted handoff", p.ID)
	}
	if err := handoff.Payload.Validate(); err != nil {
		return fmt.Errorf("phase %s accepted handoff: %w", p.ID, err)
	}
	canonical, _ := json.Marshal(handoff.Payload)
	digest := sha256.Sum256(canonical)
	if handoff.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return fmt.Errorf("phase %s has malformed or stale accepted handoff", p.ID)
	}
	return nil
}
