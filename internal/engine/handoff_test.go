package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/executiontrace"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestFreshContextCompilerProducesDeterministicBoundedReceipt(t *testing.T) {
	e := &Engine{}
	base := semanticInvocationContext{Encoding: semanticContextEncoding, Objective: "work", Dependencies: []semanticDependencyContext{}, Artifacts: []semanticArtifactReference{}, Evidence: []semanticEvidenceReference{}, Handoffs: []semanticHandoffReference{}, Validations: []semanticValidationRequirement{}}
	first, err := e.compileFreshInvocationContext(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.compileFreshInvocationContext(base)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Fresh || first.Receipt == nil || first.Receipt.Bytes > invocationContextCeiling || first.Receipt.Digest == "" {
		t.Fatalf("fresh receipt = %#v", first.Receipt)
	}
	a, _ := json.Marshal(first)
	if first.Receipt.Bytes != len(a) {
		t.Fatalf("receipt bytes = %d, encoded context = %d", first.Receipt.Bytes, len(a))
	}
	unsigned := first
	unsigned.Receipt = &semanticContextReceipt{CompilerVersion: first.Receipt.CompilerVersion, Selected: first.Receipt.Selected, Omitted: first.Receipt.Omitted}
	unsignedBytes, _ := json.Marshal(unsigned)
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(unsignedBytes))
	if first.Receipt.Digest != wantDigest {
		t.Fatalf("receipt digest = %q, want %q", first.Receipt.Digest, wantDigest)
	}
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("fresh compilation is not deterministic")
	}
	projected := projectInvocationContext(first)
	if projected.Version != provider.InvocationContextVersionV2 {
		t.Fatalf("projected version = %q", projected.Version)
	}
	projectedBytes, err := json.Marshal(projected)
	if err != nil || len(projectedBytes) > invocationContextCeiling {
		t.Fatalf("projected context exceeds ceiling: %d, err=%v", len(projectedBytes), err)
	}
}

func TestFreshContextCompilerBacktracksOptionalClosureAtFinalReceiptBoundary(t *testing.T) {
	e := &Engine{}
	base := semanticInvocationContext{
		Encoding:     semanticContextEncoding,
		Dependencies: []semanticDependencyContext{{Phase: "dependency", Commit: strings.Repeat("a", 40)}},
		Artifacts:    []semanticArtifactReference{{Name: "result", Producer: "dependency", Type: "files", Path: "result.txt", Digest: "sha256:" + strings.Repeat("b", 64), Mode: 0o100644}},
		Evidence:     []semanticEvidenceReference{}, Handoffs: []semanticHandoffReference{}, Validations: []semanticValidationRequirement{},
	}
	found := false
	for size := invocationContextCeiling - 1500; size < invocationContextCeiling; size++ {
		base.Objective = strings.Repeat("x", size)
		compiled, err := e.compileFreshInvocationContext(base)
		if err != nil {
			continue
		}
		encoded, marshalErr := json.Marshal(compiled)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if len(compiled.Receipt.Omitted) != 0 {
			found = true
			if compiled.Receipt.Bytes != len(encoded) || len(encoded) > invocationContextCeiling || len(compiled.Dependencies) != 0 || len(compiled.Artifacts) != 0 {
				t.Fatalf("backtracked context is not a bounded closure: receipt=%#v bytes=%d dependencies=%#v artifacts=%#v", compiled.Receipt, len(encoded), compiled.Dependencies, compiled.Artifacts)
			}
			break
		}
	}
	if !found {
		t.Fatal("did not exercise optional-closure backtracking at the finalized receipt boundary")
	}
}

func TestPublishAcceptedHandoffRejectsTamperedStagedPayload(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{skipPhaseFile: true}
	e := newTypedContractEngine(t, repo, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	e.nodeExecutionID = "node_00112233445566778899aabbccddeeff"
	phase, err := e.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"complete","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`)
	if err := e.stageHandoff(phase, raw, nil); err != nil {
		t.Fatal(err)
	}
	var staged AcceptedHandoff
	if ok, err := e.Store.GetJSON(e.stagedHandoffRecord(phase.ID), &staged); err != nil || !ok {
		t.Fatalf("staged handoff: ok=%t err=%v", ok, err)
	}
	staged.Payload.Summary = "tampered"
	if err := e.Store.SetJSON(e.stagedHandoffRecord(phase.ID), staged); err != nil {
		t.Fatal(err)
	}
	head, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.publishAcceptedHandoff(phase, head); err == nil || !strings.Contains(err.Error(), "digest does not match") {
		t.Fatalf("publishAcceptedHandoff() error = %v", err)
	}
}

func TestPublishAcceptedHandoffValidatesSemanticEncodingWithoutProviderProjection(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{skipPhaseFile: true}
	e := newTypedContractEngine(t, repo, p)
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	e.nodeExecutionID = "node_00112233445566778899aabbccddeeff"
	phase, err := e.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"complete","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`)
	if err := e.stageHandoff(phase, raw, nil); err != nil {
		t.Fatal(err)
	}
	var staged AcceptedHandoff
	if ok, err := e.Store.GetJSON(e.stagedHandoffRecord(phase.ID), &staged); err != nil || !ok {
		t.Fatalf("staged handoff: ok=%t err=%v", ok, err)
	}
	staged.Payload.Encoding = "agentflow.dev/semantic/v999"
	canonical, err := json.Marshal(staged.Payload)
	if err != nil {
		t.Fatal(err)
	}
	staged.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if err := e.Store.SetJSON(e.stagedHandoffRecord(phase.ID), staged); err != nil {
		t.Fatal(err)
	}
	head, err := e.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.publishAcceptedHandoff(phase, head); err == nil || !strings.Contains(err.Error(), "semantic handoff encoding") {
		t.Fatalf("publishAcceptedHandoff() error = %v, want semantic encoding rejection", err)
	}
}

func TestCompileAcceptedHandoffsRejectsForeignRun(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "implement" {
			return nil
		}
		return writeTypedContractResult(request.Workspace)
	}}
	e := newTypedContractEngine(t, repo, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var accepted AcceptedHandoff
	if ok, err := e.Store.GetJSON(e.acceptedHandoffRecord("implement"), &accepted); err != nil || !ok {
		t.Fatalf("accepted handoff: ok=%t err=%v", ok, err)
	}
	accepted.RunID = "run_ffeeddccbbaa99887766554433221100"
	if err := e.Store.SetJSON(e.acceptedHandoffRecord("implement"), accepted); err != nil {
		t.Fatal(err)
	}
	consumer, err := e.phaseByID("audit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.compileAcceptedHandoffs(consumer); err == nil || !strings.Contains(err.Error(), "malformed or stale") {
		t.Fatalf("compileAcceptedHandoffs() error = %v", err)
	}
	accepted.RunID = e.runID
	accepted.Payload.Encoding = "agentflow.dev/semantic/v999"
	canonical, err := json.Marshal(accepted.Payload)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if err := e.Store.SetJSON(e.acceptedHandoffRecord("implement"), accepted); err != nil {
		t.Fatal(err)
	}
	if _, err := e.compileAcceptedHandoffs(consumer); err == nil || !strings.Contains(err.Error(), "semantic handoff encoding") {
		t.Fatalf("compileAcceptedHandoffs() error = %v, want semantic encoding rejection", err)
	}
}

func TestRecoverActiveRejectsSemanticallyInvalidAcceptedHandoff(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] != "implement" {
			return nil
		}
		return writeTypedContractResult(request.Workspace)
	}}
	e := newTypedContractEngine(t, repo, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	phase, err := e.phaseByID("implement")
	if err != nil {
		t.Fatal(err)
	}
	commit, ok, err := e.Store.Resolve(e.phaseMarkerName(phase))
	if err != nil || !ok {
		t.Fatalf("phase marker: commit=%q ok=%t err=%v", commit, ok, err)
	}
	var accepted AcceptedHandoff
	if ok, err := e.Store.GetJSON(e.acceptedHandoffRecord(phase.ID), &accepted); err != nil || !ok {
		t.Fatalf("accepted handoff: ok=%t err=%v", ok, err)
	}
	accepted.Payload.Encoding = "agentflow.dev/semantic/v999"
	canonical, err := json.Marshal(accepted.Payload)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	if err := e.Store.SetJSON(e.acceptedHandoffRecord(phase.ID), accepted); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.activeRecord(), ActivePhase{
		PhaseID: phase.ID, StartCommit: commit, ActorCompleted: true,
		NodeExecutionID: accepted.NodeExecutionID, Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.recoverActive(context.Background()); err == nil || !strings.Contains(err.Error(), "semantic handoff encoding") {
		t.Fatalf("recoverActive() error = %v, want semantic encoding rejection", err)
	}
}

func TestV1ConsumerDoesNotLoadAcceptedV2Handoff(t *testing.T) {
	repo := newDurableRepo(t)
	producer := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["phase"] == "implement" {
			return writeTypedContractResult(request.Workspace)
		}
		return nil
	}}
	template := newTypedContractEngine(t, repo, producer)
	template.Workflow.Spec.Agents["auditor"] = workflow.Agent{Runner: "legacy", Model: "legacy", MayCommit: false}
	template.Workflow.Spec.Phases[1].Kind = "implementation"
	legacy := &schedulingProvider{skipPhaseFile: true}
	e, err := New(template.Workflow, map[string]provider.Provider{"test": producer, "legacy": legacy}, Options{RepoRoot: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(legacy.contexts) != 1 || legacy.contexts[0].Version != provider.InvocationContextVersionV1 || len(legacy.contexts[0].Handoffs) != 0 {
		t.Fatalf("legacy consumer context = %#v", legacy.contexts)
	}
}

func TestV2CapableProviderUsesV1ContextForOrdinaryPhase(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{structured: true}
	w := schedulingWorkflow(repo, "ordinary-v1-context", []string{"ordinary"}, nil, "true")
	e := newSchedulingEngine(t, w, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.contexts) != 1 {
		t.Fatalf("provider contexts = %d, want 1", len(p.contexts))
	}
	got := p.contexts[0]
	if got.Version != provider.InvocationContextVersionV1 || got.Receipt != nil || len(got.Handoffs) != 0 {
		t.Fatalf("ordinary context = %#v, want exact v1 context without receipt or handoffs", got)
	}
}

func TestStructuredPhaseUsesV2ContextWithReceipt(t *testing.T) {
	repo := newDurableRepo(t)
	p := &schedulingProvider{structured: true}
	w := schedulingWorkflow(repo, "structured-v2-context", []string{"audit"}, nil, "true")
	w.Spec.Phases[0].Kind = "audit"
	w.Spec.Phases[0].RequiresChange = false
	e := newSchedulingEngine(t, w, p)
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.contexts) != 1 {
		t.Fatalf("provider contexts = %d, want 1", len(p.contexts))
	}
	got := p.contexts[0]
	if got.Version != provider.InvocationContextVersionV2 || got.Receipt == nil || got.Receipt.Digest == "" || got.Receipt.Bytes <= 0 {
		t.Fatalf("structured context = %#v, want v2 context with compilation receipt", got)
	}
	events := readExecutionTrace(t, e)
	requireTraceEvent(t, events, "context_compiled", func(event executiontrace.Event) bool {
		return event.Fields["context_version"] == provider.InvocationContextVersionV2 &&
			event.Fields["context_bytes"] == strconv.Itoa(got.Receipt.Bytes) &&
			event.Fields["context_selected_count"] == strconv.Itoa(len(got.Receipt.Selected)) &&
			event.Fields["context_omitted_count"] == strconv.Itoa(len(got.Receipt.Omitted)) &&
			event.Fields["context_digest"] == got.Receipt.Digest
	})
	var accepted AcceptedHandoff
	if ok, err := e.Store.GetJSON(e.acceptedHandoffRecord("audit"), &accepted); err != nil || !ok {
		t.Fatalf("accepted handoff: ok=%t err=%v", ok, err)
	}
	requireTraceEvent(t, events, "handoff_staged", func(event executiontrace.Event) bool {
		return event.Fields["handoff_digest"] == accepted.Digest && event.Fields["handoff_status"] == "complete"
	})
	requireTraceEvent(t, events, "handoff_accepted", func(event executiontrace.Event) bool {
		return event.Fields["handoff_digest"] == accepted.Digest
	})
}

func writeTypedContractResult(workspace string) error {
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "src", "result.txt"), []byte("accepted\n"), 0o644)
}
