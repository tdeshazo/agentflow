package engine

import (
	"path/filepath"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestRunIdentityPreservesLegacyWorkflowDigestWithoutDefaults(t *testing.T) {
	const legacyPriority4WorkflowDigest = "92f83c88a0c70b76ebf3ae2364e28da2f18f1d5933e501423cd770593e7cf461"
	document, err := workflow.Decode(filepath.Join("..", "..", "examples", "finish-priority-04.agent-workflow.yaml"))
	if err != nil {
		t.Fatalf("decode workflow: %v", err)
	}
	e, err := New(document.Workflow, nil, Options{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	got, err := e.expectedRunIdentity()
	if err != nil {
		t.Fatalf("expected run identity: %v", err)
	}

	want, err := digestCanonicalJSON(runWorkflowDefinition{
		APIVersion: e.Workflow.APIVersion,
		Kind:       e.Workflow.Kind,
		Spec:       legacyRunIdentitySpec(document.Workflow.Spec),
	})
	if err != nil {
		t.Fatalf("legacy workflow digest: %v", err)
	}
	if got.WorkflowDigest != want {
		t.Fatalf("workflow digest = %s, want legacy-compatible %s", got.WorkflowDigest, want)
	}
	if got.WorkflowDigest != legacyPriority4WorkflowDigest {
		t.Fatalf("workflow digest = %s, want durable phase-04 identity %s", got.WorkflowDigest, legacyPriority4WorkflowDigest)
	}
}
