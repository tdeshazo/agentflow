package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const (
	parallelBatchVersion = 1
	parallelBatchRecord  = "scheduler/active-batch"
	parallelResultRecord = "scheduler-result"
)

type parallelBatch struct {
	Version  int      `json:"version"`
	ID       string   `json:"id"`
	Baseline string   `json:"baseline"`
	Phases   []string `json:"phases"`
}

type parallelNodeResult struct {
	Version   int  `json:"version"`
	Succeeded bool `json:"succeeded"`
}

type parallelCallResult struct {
	phaseID string
	err     error
}

func (e *Engine) parallelReadyBatch(ready []workflow.PhaseDependencyNode) ([]workflow.PhaseDependencyNode, error) {
	maxParallel := workflow.EffectiveMaxParallel(e.Workflow.Spec.Execution.MaxParallel)
	if maxParallel <= 1 || len(ready) < 2 {
		return ready[:1], nil
	}
	first, err := e.phaseByID(ready[0].ID)
	if err != nil {
		return nil, err
	}
	if !e.phaseParallelEligible(first) {
		return ready[:1], nil
	}
	firstScope, err := e.phaseResourceScope(first)
	if err != nil {
		return nil, err
	}
	batch := []workflow.PhaseDependencyNode{ready[0]}
	scopes := [][]string{firstScope}
	for _, node := range ready[1:] {
		if len(batch) >= maxParallel {
			break
		}
		phase, err := e.phaseByID(node.ID)
		if err != nil {
			return nil, err
		}
		if !e.phaseParallelEligible(phase) {
			continue
		}
		scope, err := e.phaseResourceScope(phase)
		if err != nil {
			return nil, err
		}
		conflict := false
		for _, selected := range scopes {
			if phaseScopesConflict(selected, scope) {
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}
		batch = append(batch, node)
		scopes = append(scopes, scope)
	}
	return batch, nil
}

func (e *Engine) phaseParallelEligible(phase *workflow.Phase) bool {
	if phase == nil || phase.If != "" || phase.IfEvidence != "" || phase.AdvanceProgress || len(phase.Bookkeeping) != 0 {
		return false
	}
	agent, ok := e.Workflow.Spec.Agents[phase.Actor]
	return ok && !e.effectiveActorCommitPermission(agent) && e.runtimeOwnsPhaseLifecycle(phase)
}

func (e *Engine) runParallelBatch(ctx context.Context, nodes []workflow.PhaseDependencyNode) error {
	previousNodeID, previousNodeExecutionID, previousNodeAttempt := e.nodeID, e.nodeExecutionID, e.nodeAttempt
	defer func() {
		e.nodeID, e.nodeExecutionID, e.nodeAttempt = previousNodeID, previousNodeExecutionID, previousNodeAttempt
	}()
	if len(nodes) < 2 {
		return fmt.Errorf("parallel scheduler requires at least two nodes")
	}
	baseline, err := e.Repo.Head()
	if err != nil {
		return err
	}
	phaseIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		phaseIDs = append(phaseIDs, node.ID)
	}
	batch := parallelBatch{
		Version:  parallelBatchVersion,
		ID:       parallelBatchID(baseline, phaseIDs),
		Baseline: baseline,
		Phases:   phaseIDs,
	}
	for _, phaseID := range phaseIDs {
		phase, err := e.phaseByID(phaseID)
		if err != nil {
			return err
		}
		if err := e.validateContractInputs(phase); err != nil {
			return fmt.Errorf("phase %s typed inputs: %w", phaseID, err)
		}
		active, err := e.newActivePhaseFor(phase)
		if err != nil {
			return err
		}
		active.ParallelBatch = batch.ID
		nodeEngine := e.parallelNodeEngine(batch.ID, phaseID)
		if err := nodeEngine.Store.SetJSON(nodeEngine.activeRecord(), active); err != nil {
			return err
		}
	}
	if err := e.Store.SetJSON(parallelBatchRecord, batch); err != nil {
		return fmt.Errorf("persist parallel scheduler batch: %w", err)
	}

	e.presenter().SchedulerBatchStart(phaseIDs)
	results := make(chan parallelCallResult, len(phaseIDs))
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(len(phaseIDs))
	for _, phaseID := range phaseIDs {
		phaseID := phaseID
		go func() {
			defer workers.Done()
			nodeEngine := e.parallelNodeEngine(batch.ID, phaseID)
			phase, phaseErr := nodeEngine.phaseByID(phaseID)
			if phaseErr == nil {
				phaseErr = nodeEngine.runAgentWithRole(
					workerContext,
					phase.Actor,
					phase.Reasoning,
					phase.Prompt,
					invocationRolePhase,
					phase,
				)
			}
			if phaseErr == nil {
				phaseErr = nodeEngine.Store.SetJSON(
					nodeEngine.scopedRecord(parallelResultRecord),
					parallelNodeResult{Version: parallelBatchVersion, Succeeded: true},
				)
			}
			if phaseErr != nil {
				cancel()
			}
			results <- parallelCallResult{phaseID: phaseID, err: phaseErr}
		}()
	}
	workers.Wait()
	close(results)
	callErrors := make(map[string]error, len(phaseIDs))
	for result := range results {
		callErrors[result.phaseID] = result.err
	}
	return e.acceptParallelBatch(ctx, batch, callErrors)
}

func (e *Engine) recoverParallelBatch(ctx context.Context) error {
	var batch parallelBatch
	ok, err := e.Store.GetJSON(parallelBatchRecord, &batch)
	if err != nil || !ok {
		return err
	}
	if err := e.validateParallelBatch(batch); err != nil {
		return err
	}
	e.presenter().SchedulerBatchResume(batch.Phases)
	return e.acceptParallelBatch(ctx, batch, nil)
}

func (e *Engine) acceptParallelBatch(ctx context.Context, batch parallelBatch, callErrors map[string]error) error {
	previousNodeID, previousNodeExecutionID, previousNodeAttempt := e.nodeID, e.nodeExecutionID, e.nodeAttempt
	defer func() {
		e.nodeID, e.nodeExecutionID, e.nodeAttempt = previousNodeID, previousNodeExecutionID, previousNodeAttempt
	}()
	for _, phaseID := range batch.Phases {
		phase, err := e.phaseByID(phaseID)
		if err != nil {
			return err
		}
		if accepted, err := e.phaseDependencyAccepted(phaseID); err != nil {
			return err
		} else if accepted {
			if err := e.clearParallelNode(batch.ID, phaseID); err != nil {
				return err
			}
			continue
		}

		nodeEngine := e.parallelNodeEngine(batch.ID, phaseID)
		if _, pending, err := nodeEngine.Store.Resolve(nodeEngine.pendingInvocationRecord()); err != nil {
			return err
		} else if pending {
			if _, err := nodeEngine.reconcilePendingInvocation(); err != nil {
				if copyErr := e.promoteParallelActive(nodeEngine, phaseID); copyErr != nil {
					return errors.Join(err, copyErr)
				}
				return err
			}
		}
		var active ActivePhase
		if ok, err := nodeEngine.Store.GetJSON(nodeEngine.activeRecord(), &active); err != nil {
			return err
		} else if !ok || active.PhaseID != phaseID || active.ParallelBatch != batch.ID {
			return fmt.Errorf("parallel scheduler phase %s has no compatible active state", phaseID)
		}
		var outcome ActorInvocationOutcome
		if ok, err := nodeEngine.Store.GetJSON(nodeEngine.invocationOutcomeRecord(), &outcome); err != nil {
			return err
		} else if ok {
			active.ActorChangedPaths = append([]string(nil), outcome.ChangedPaths...)
		}
		var result parallelNodeResult
		resultOK, err := nodeEngine.Store.GetJSON(nodeEngine.scopedRecord(parallelResultRecord), &result)
		if err != nil {
			return err
		}
		if resultOK && result.Version != parallelBatchVersion {
			return fmt.Errorf("unsupported parallel scheduler result version %d", result.Version)
		}
		active.ActorCompleted = resultOK && result.Succeeded
		e.nodeID, e.nodeExecutionID, e.nodeAttempt = active.PhaseID, active.NodeExecutionID, active.Attempt
		if err := nodeEngine.Store.SetJSON(nodeEngine.activeRecord(), active); err != nil {
			return err
		}
		if err := e.Store.SetJSON(e.activeRecord(), active); err != nil {
			return err
		}
		e.recoveryEligible = true
		if callErr := preferredParallelCallError(phaseID, batch.Phases, callErrors); callErr != nil {
			return callErr
		}
		if !active.ActorCompleted {
			if err := e.recoverActive(ctx); err != nil {
				return err
			}
		} else if err := e.finishPhase(ctx, phase, active); err != nil {
			return err
		}
		if err := e.clearParallelNode(batch.ID, phaseID); err != nil {
			return err
		}
	}
	return e.Store.Delete(parallelBatchRecord)
}

func preferredParallelCallError(phaseID string, phases []string, callErrors map[string]error) error {
	callErr := callErrors[phaseID]
	if callErr == nil || !errors.Is(callErr, context.Canceled) {
		return callErr
	}
	for _, candidate := range phases {
		candidateErr := callErrors[candidate]
		if candidateErr != nil && !errors.Is(candidateErr, context.Canceled) {
			return candidateErr
		}
	}
	return callErr
}

func (e *Engine) promoteParallelActive(nodeEngine *Engine, phaseID string) error {
	var active ActivePhase
	ok, err := nodeEngine.Store.GetJSON(nodeEngine.activeRecord(), &active)
	if err != nil {
		return err
	}
	if !ok || active.PhaseID != phaseID {
		return fmt.Errorf("parallel scheduler phase %s has no active state", phaseID)
	}
	return e.Store.SetJSON(e.activeRecord(), active)
}

func (e *Engine) parallelNodeEngine(batchID, phaseID string) *Engine {
	clone := *e
	clone.recordScope = parallelNodeScope(batchID, phaseID)
	clone.parallelReconcile = true
	clone.deferReconciliation = true
	clone.Out = io.Discard
	clone.logStore = nil
	clone.outputBridge = nil
	clone.outputRestore = nil
	clone.phase = nil
	return &clone
}

func (e *Engine) clearParallelNode(batchID, phaseID string) error {
	nodeEngine := e.parallelNodeEngine(batchID, phaseID)
	for _, record := range []string{
		nodeEngine.activeRecord(),
		nodeEngine.pendingInvocationRecord(),
		nodeEngine.invocationOutcomeRecord(),
		nodeEngine.scopedRecord(parallelResultRecord),
	} {
		if err := nodeEngine.Store.Delete(record); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) validateParallelBatch(batch parallelBatch) error {
	if batch.Version != parallelBatchVersion {
		return fmt.Errorf("unsupported parallel scheduler batch version %d", batch.Version)
	}
	if batch.ID == "" || batch.Baseline == "" || !e.Repo.ObjectExists(batch.Baseline+"^{commit}") {
		return fmt.Errorf("parallel scheduler batch is incomplete")
	}
	if len(batch.Phases) < 2 || len(batch.Phases) > workflow.MaxParallelPhases {
		return fmt.Errorf("parallel scheduler batch has invalid phase count %d", len(batch.Phases))
	}
	if batch.ID != parallelBatchID(batch.Baseline, batch.Phases) {
		return fmt.Errorf("parallel scheduler batch identity does not match its graph state")
	}
	seen := make(map[string]bool, len(batch.Phases))
	for _, phaseID := range batch.Phases {
		if seen[phaseID] {
			return fmt.Errorf("parallel scheduler batch repeats phase %q", phaseID)
		}
		seen[phaseID] = true
		phase, err := e.phaseByID(phaseID)
		if err != nil {
			return err
		}
		if !e.phaseParallelEligible(phase) {
			return fmt.Errorf("phase %s is no longer eligible for parallel recovery", phaseID)
		}
	}
	return nil
}

func parallelBatchID(baseline string, phases []string) string {
	digest := sha256.Sum256([]byte(baseline + "\x00" + strings.Join(phases, "\x00")))
	return hex.EncodeToString(digest[:16])
}

func parallelNodeScope(batchID, phaseID string) string {
	return "scheduler/nodes/" + batchID + "/" + hex.EncodeToString([]byte(phaseID))
}
