package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tdeshazo/agentflow/internal/executiontrace"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestRunAndNodeExecutionIdentitiesSurviveRecovery(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "stable-execution-identities")
	firstProvider := &durableProvider{action: func(context.Context, provider.Request) error {
		return errors.New("interrupted provider")
	}}
	first := newDurableEngine(t, w, firstProvider)
	if err := first.Run(context.Background()); err == nil {
		t.Fatal("first run unexpectedly succeeded")
	}
	var identity RunIdentity
	if ok, err := first.Store.GetJSON(first.runIdentityRecord(), &identity); err != nil || !ok {
		t.Fatalf("run identity: ok=%v err=%v", ok, err)
	}
	if identity.Version != runIdentityVersion || identity.RunID == "" {
		t.Fatalf("run identity = %+v", identity)
	}
	var interrupted ActivePhase
	if ok, err := first.Store.GetJSON(first.activeRecord(), &interrupted); err != nil || !ok {
		t.Fatalf("active phase: ok=%v err=%v", ok, err)
	}
	if interrupted.NodeExecutionID == "" || interrupted.Attempt != 1 {
		t.Fatalf("active node identity = %+v", interrupted)
	}

	var providerMetadata map[string]string
	restarted := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
		providerMetadata = request.Metadata
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}})
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if providerMetadata["run_id"] != identity.RunID || providerMetadata["node_execution_id"] != interrupted.NodeExecutionID {
		t.Fatalf("provider metadata = %#v", providerMetadata)
	}
	snapshot, err := restarted.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunID != identity.RunID || snapshot.TraceSchemaVersion != executiontrace.SchemaVersion || snapshot.TracePath == "" {
		t.Fatalf("status identity = %+v", snapshot)
	}
	assertTraceIdentities(t, snapshot.TracePath, identity.RunID, interrupted.NodeExecutionID)
	events := readExecutionTrace(t, restarted)
	requireTraceEvent(t, events, "node_attempt_resumed", func(event executiontrace.Event) bool {
		return event.NodeID == interrupted.PhaseID && event.NodeExecutionID == interrupted.NodeExecutionID && event.Attempt == interrupted.Attempt
	})
	requireTraceEvent(t, events, "provider_response", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == interrupted.NodeExecutionID && event.Fields["result"] == "failure" && event.Fields["outcome"] == "error"
	})
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "interrupted provider") {
		t.Fatal("trace persisted provider error output")
	}
}

func TestLegacyRunIdentityMigratesWithoutChangingCompatibilityDigests(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "legacy-run-identity-migration")
	w.Spec.Flow = nil
	first := newDurableEngine(t, w, &durableProvider{})
	if err := first.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	var legacy RunIdentity
	if ok, err := first.Store.GetJSON(first.runIdentityRecord(), &legacy); err != nil || !ok {
		t.Fatalf("run identity: ok=%v err=%v", ok, err)
	}
	legacy.Version = legacyRunIdentityVersion
	legacy.RunID = ""
	if err := first.Store.SetJSON(first.runIdentityRecord(), legacy); err != nil {
		t.Fatal(err)
	}

	restarted := newDurableEngine(t, w, &durableProvider{})
	if err := restarted.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	var migrated RunIdentity
	if ok, err := restarted.Store.GetJSON(restarted.runIdentityRecord(), &migrated); err != nil || !ok {
		t.Fatalf("migrated identity: ok=%v err=%v", ok, err)
	}
	if migrated.Version != runIdentityVersion || !validStableID(migrated.RunID, "run") {
		t.Fatalf("migrated identity = %+v", migrated)
	}
	if migrated.WorkflowDigest != legacy.WorkflowDigest || migrated.ParametersDigest != legacy.ParametersDigest || migrated.ExecutionDigest != legacy.ExecutionDigest {
		t.Fatalf("migration changed compatibility digests: before=%+v after=%+v", legacy, migrated)
	}
}

func TestParallelTraceKeepsNodeExecutionAttributionIsolated(t *testing.T) {
	repo := newDurableRepo(t)
	w := schedulingWorkflow(repo, "parallel-trace-identities", []string{"left", "right"}, nil, "true")
	w.Spec.Execution.MaxParallel = 2
	w.Spec.Workspace.MutationPolicy.Allowed = []string{"left/**", "right/**"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
	w.Spec.Phases[0].Writes = []string{"left/**"}
	w.Spec.Phases[1].Writes = []string{"right/**"}
	identities := map[string]string{}
	var identitiesMu sync.Mutex
	providerImpl := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		phaseID := request.Metadata["phase"]
		identitiesMu.Lock()
		identities[phaseID] = request.Metadata["node_execution_id"]
		identitiesMu.Unlock()
		path := filepath.Join(request.Workspace, phaseID, "result.txt")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(phaseID+"\n"), 0o644)
	}}
	e := newSchedulingEngine(t, w, providerImpl)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if identities["left"] == "" || identities["right"] == "" || identities["left"] == identities["right"] {
		t.Fatalf("parallel node identities = %#v", identities)
	}
	snapshot, err := e.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	assertParallelValidationTrace(t, snapshot.TracePath, identities)
}

func TestTraceCoversDurablePhaseAndCompletionTransitions(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "complete-trace-lifecycle")
	e := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	phaseCommit, ok, err := e.Store.Resolve("phases/change")
	if err != nil || !ok {
		t.Fatalf("phase evidence: commit=%q ok=%v err=%v", phaseCommit, ok, err)
	}
	completionCommit, ok, err := e.Store.Resolve("complete")
	if err != nil || !ok {
		t.Fatalf("completion evidence: commit=%q ok=%v err=%v", completionCommit, ok, err)
	}
	events := readExecutionTrace(t, e)
	started := requireTraceEvent(t, events, "node_attempt_started", func(event executiontrace.Event) bool {
		return event.NodeID == "change" && event.NodeExecutionID != "" && event.Attempt == 1 && event.Fields["start_commit"] != ""
	})
	requireTraceEvent(t, events, "validation_end", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["validation"] == "phaseGate" && event.Fields["result"] == "success"
	})
	requireTraceEvent(t, events, "checkpoint_end", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["commit"] == phaseCommit && event.Fields["result"] == "success"
	})
	requireTraceEvent(t, events, "phase_accepted", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["record"] == opaqueTraceRecord("phases/change") && event.Fields["commit"] == phaseCommit
	})
	requireTraceEvent(t, events, "node_attempt_finished", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["result"] == "success"
	})
	requireTraceEvent(t, events, "completion_evidence", func(event executiontrace.Event) bool {
		return event.Fields["completion"] == "done" && event.Fields["record"] == opaqueTraceRecord("complete") && event.Fields["commit"] == completionCommit
	})
}

func TestTraceCoversValidationRepairAttemptAndOutcome(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "complete-trace-repair")
	w.Spec.Validation["phaseGate"] = repairValidation()
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
	e := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
		contents := "partial\n"
		if request.Metadata["actor"] == "repair" {
			contents = "complete\n"
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte(contents), 0o644)
	}})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := readExecutionTrace(t, e)
	requireTraceEvent(t, events, "validation_failed", func(event executiontrace.Event) bool {
		return event.Fields["validation"] == "phaseGate" && event.Fields["failure_kind"] == string(PhaseFailureValidation)
	})
	requireTraceEvent(t, events, "repair_attempt_start", func(event executiontrace.Event) bool {
		return event.Fields["validation"] == "phaseGate" && event.Fields["actor"] == "repair" && event.Fields["repair_attempt"] == "1" && event.Fields["max_attempts"] == "1"
	})
	requireTraceEvent(t, events, "repair_attempt_end", func(event executiontrace.Event) bool {
		return event.Fields["validation"] == "phaseGate" && event.Fields["repair_attempt"] == "1" && event.Fields["result"] == "success"
	})
	requireTraceEvent(t, events, "validation_repaired", func(event executiontrace.Event) bool {
		return event.Fields["validation"] == "phaseGate" && event.Fields["repair_attempt"] == "1"
	})
	for _, event := range events {
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "partial") {
			t.Fatalf("trace leaked validation output: %s", encoded)
		}
	}
}

func TestTraceCoversDurableRepairExhaustion(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "complete-trace-repair-exhaustion")
	w.Spec.Validation["phaseGate"] = repairValidation()
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test"}
	providerImpl := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
	}}
	first := newDurableEngine(t, w, providerImpl)
	if err := first.Run(context.Background()); err == nil {
		t.Fatal("first run unexpectedly succeeded")
	}
	restarted := newDurableEngine(t, w, providerImpl)
	if err := restarted.Run(context.Background()); err == nil {
		t.Fatal("restart unexpectedly renewed the repair budget")
	}
	events := readExecutionTrace(t, restarted)
	requireTraceEvent(t, events, "repair_budget_exhausted", func(event executiontrace.Event) bool {
		return event.Fields["validation"] == "phaseGate" && event.Fields["max_attempts"] == "1"
	})
	requireTraceEvent(t, events, "node_attempt_blocked", func(event executiontrace.Event) bool {
		return event.NodeID == "change" && event.Fields["failure_kind"] == string(PhaseFailureValidation) && event.Fields["result"] == "failure"
	})
}

func TestTraceCoversHumanGateAndCompletionEvidence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "complete-trace-human")
	w.Spec.Phases = nil
	w.Spec.Flow = []workflow.FlowStep{{Human: "review"}, {Complete: "done"}}
	w.Spec.HumanGates = []workflow.HumanGate{{
		ID: "review", When: "{{ true }}",
		Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "yes"},
	}}
	e := newDurableEngine(t, w, &durableProvider{})
	e.In = strings.NewReader("yes\n")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	humanCommit, ok, err := e.Store.Resolve("human/review")
	if err != nil || !ok {
		t.Fatalf("human evidence: commit=%q ok=%v err=%v", humanCommit, ok, err)
	}
	events := readExecutionTrace(t, e)
	requireTraceEvent(t, events, "human_gate_evidence", func(event executiontrace.Event) bool {
		return event.Fields["gate"] == "review" && event.Fields["decision"] == "confirmed" && event.Fields["record"] == opaqueTraceRecord("human/review") && event.Fields["commit"] == humanCommit
	})
	requireTraceEvent(t, events, "completion_evidence", func(event executiontrace.Event) bool {
		return event.Fields["completion"] == "done" && event.Fields["record"] == opaqueTraceRecord("complete") && event.Fields["commit"] == humanCommit
	})
}

func TestTraceCoversConditionallySkippedNodeEvidence(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "complete-trace-skip")
	w.Spec.Phases[0].If = "{{ false }}"
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	commit, ok, err := e.Store.Resolve("phases/change")
	if err != nil || !ok {
		t.Fatalf("skip evidence: commit=%q ok=%v err=%v", commit, ok, err)
	}
	events := readExecutionTrace(t, e)
	started := requireTraceEvent(t, events, "node_attempt_started", func(event executiontrace.Event) bool {
		return event.NodeID == "change" && event.NodeExecutionID != "" && event.Attempt == 1
	})
	requireTraceEvent(t, events, "node_skipped", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["record"] == opaqueTraceRecord("phases/change") && event.Fields["commit"] == commit && event.Fields["reason"] == "condition is false"
	})
	requireTraceEvent(t, events, "node_attempt_finished", func(event executiontrace.Event) bool {
		return event.NodeExecutionID == started.NodeExecutionID && event.Fields["result"] == "skipped"
	})
}

func TestTraceUsesOpaqueReferencesForResolvedRecordNames(t *testing.T) {
	repo := newDurableRepo(t)
	const (
		parameterValue   = "customer-secret-rose"
		phaseEnvValue    = "phase-secret-sable"
		humanEnvValue    = "human-secret-amber"
		completeEnvValue = "completion-secret-ivory"
	)
	t.Setenv("AGENTFLOW_TRACE_PHASE_RECORD", phaseEnvValue)
	t.Setenv("AGENTFLOW_TRACE_HUMAN_RECORD", humanEnvValue)
	t.Setenv("AGENTFLOW_TRACE_COMPLETION_RECORD", completeEnvValue)
	w := durableWorkflow(repo, "opaque-trace-records")
	w.Spec.Parameters["customer"] = workflow.Parameter{Type: "string", Default: parameterValue}
	w.Spec.State.Records.CompletedPhasePattern = "phases/{{ parameters.customer }}/{{ env.AGENTFLOW_TRACE_PHASE_RECORD }}/{{ phase.id }}"
	w.Spec.Flow = []workflow.FlowStep{{Phase: "change"}, {Human: "review"}, {Complete: "done"}}
	w.Spec.HumanGates = []workflow.HumanGate{{
		ID: "review", When: "{{ true }}",
		Acknowledgement: workflow.Acknowledgement{Type: "exact-text", Value: "yes"},
		Evidence:        workflow.Marker{Record: "human/{{ parameters.customer }}/{{ env.AGENTFLOW_TRACE_HUMAN_RECORD }}"},
	}}
	completion := w.Spec.Completion["done"]
	completion.WriteMarker.Record = "completion/{{ parameters.customer }}/{{ env.AGENTFLOW_TRACE_COMPLETION_RECORD }}"
	w.Spec.Completion["done"] = completion
	e := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
	}})
	e.In = strings.NewReader("yes\n")
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	phaseRecord := "phases/" + parameterValue + "/" + phaseEnvValue + "/change"
	humanRecord := "human/" + parameterValue + "/" + humanEnvValue
	completionRecord := "completion/" + parameterValue + "/" + completeEnvValue
	for _, expected := range []struct {
		name   string
		record string
	}{
		{name: "phase", record: phaseRecord},
		{name: "human", record: humanRecord},
		{name: "completion", record: completionRecord},
	} {
		t.Run("authoritative_"+expected.name, func(t *testing.T) {
			if _, ok, err := e.Store.Resolve(expected.record); err != nil || !ok {
				t.Fatalf("authoritative record was not persisted: ok=%v err=%v", ok, err)
			}
		})
	}

	events := readExecutionTrace(t, e)
	for _, expected := range []struct {
		kind   string
		record string
	}{
		{kind: "phase_accepted", record: phaseRecord},
		{kind: "human_gate_evidence", record: humanRecord},
		{kind: "completion_evidence", record: completionRecord},
	} {
		t.Run("trace_"+expected.kind, func(t *testing.T) {
			requireTraceEvent(t, events, expected.kind, func(event executiontrace.Event) bool {
				return event.Fields["record"] == opaqueTraceRecord(expected.record)
			})
		})
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []struct {
		name  string
		value string
	}{
		{name: "parameter", value: parameterValue},
		{name: "phase_environment", value: phaseEnvValue},
		{name: "human_environment", value: humanEnvValue},
		{name: "completion_environment", value: completeEnvValue},
	} {
		t.Run("excludes_"+secret.name, func(t *testing.T) {
			if strings.Contains(string(encoded), secret.value) {
				t.Fatal("trace persisted a resolved record value")
			}
		})
	}

	restarted := newDurableEngine(t, w, &durableProvider{})
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	restartedEvents := readExecutionTrace(t, restarted)
	requireTraceEvent(t, restartedEvents, "completion_evidence", func(event executiontrace.Event) bool {
		return event.Fields["decision"] == "reused" && event.Fields["record"] == opaqueTraceRecord(restarted.workflowCompleteMarker())
	})
}

func readExecutionTrace(t *testing.T, e *Engine) []executiontrace.Event {
	t.Helper()
	snapshot, err := e.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(snapshot.TracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []executiontrace.Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event executiontrace.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func requireTraceEvent(t *testing.T, events []executiontrace.Event, kind string, matches func(executiontrace.Event) bool) executiontrace.Event {
	t.Helper()
	for _, event := range events {
		if event.Event == kind && matches(event) {
			return event
		}
	}
	t.Fatalf("trace has no matching %q event: %+v", kind, events)
	return executiontrace.Event{}
}

func assertParallelValidationTrace(t *testing.T, path string, identities map[string]string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	found := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "interrupted provider") {
			t.Fatalf("trace persisted provider error output: %s", scanner.Text())
		}
		var event executiontrace.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event != "validation_start" {
			continue
		}
		if want := identities[event.NodeID]; want != "" && event.NodeExecutionID == want {
			found[event.NodeID] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !found["left"] || !found["right"] {
		t.Fatalf("parallel validation attribution = %#v, want both nodes", found)
	}
}

func assertTraceIdentities(t *testing.T, path, runID, nodeExecutionID string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var lastSequence uint64
	foundNode := false
	for scanner.Scan() {
		var event executiontrace.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.SchemaVersion != executiontrace.SchemaVersion || event.RunID != runID || event.Sequence != lastSequence+1 {
			t.Fatalf("trace event = %+v", event)
		}
		lastSequence = event.Sequence
		if event.NodeExecutionID == nodeExecutionID {
			foundNode = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !foundNode {
		t.Fatalf("trace has no event for node execution %q", nodeExecutionID)
	}
}
