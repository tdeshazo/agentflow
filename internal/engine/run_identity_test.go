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
