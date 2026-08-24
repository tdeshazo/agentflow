package workflow

import (
	"strings"
	"testing"
)

func TestReservedInlineActorRejectsAliasedRepairActor(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: alias-repair-bypass
  description: &internal_actor __inline_actor__build
spec:
  defaults:
    agent:
      runner: codex
    repair:
      actor: *internal_actor
      reasoning: high
      prompt: Repair the failure.
  validation:
    gate:
      run: "false"
      repair: once
  phases:
    - id: build
      kind: implementation
      actor:
        model: build-model
      validation: gate
      prompt: Build.
`))
	if err == nil || !strings.Contains(err.Error(), `defaults.repair.actor references reserved inline actor name "__inline_actor__build"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestReservedInlineActorRejectsAliasedPhaseActor(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: alias-phase-bypass
  description: &internal_actor __inline_actor__build
spec:
  defaults:
    agent:
      runner: codex
  validation:
    gate:
      run: "true"
  phases:
    - id: build
      kind: implementation
      actor:
        model: build-model
      validation: gate
      prompt: Build.
    - id: review
      kind: audit
      actor: *internal_actor
      validation: gate
      prompt: Review.
`))
	if err == nil || !strings.Contains(err.Error(), `phases[1].actor references reserved inline actor name "__inline_actor__build"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestReservedInlineActorRejectsAliasedAgentName(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: alias-agent-name
  description: &internal_actor __inline_actor__build
spec:
  agents:
    *internal_actor:
      runner: codex
`))
	if err == nil || !strings.Contains(err.Error(), `agent name "__inline_actor__build" uses reserved prefix "__inline_actor__"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestOrdinaryWorkflowActorAliasStillUsesOriginalDecodePath(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: ordinary-actor-alias
  description: &reviewer_name reviewer
spec:
  agents:
    reviewer:
      runner: codex
  tools:
    gate:
      type: shell
      command: "true"
  validation:
    gate:
      steps:
        - uses: gate
  phases:
    - id: review
      kind: audit
      actor: *reviewer_name
      validation: gate
      prompt: Review.
  flow:
    - phase: review
`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Workflow.Spec.Phases[0].Actor != "reviewer" {
		t.Fatalf("review actor = %q", d.Workflow.Spec.Phases[0].Actor)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
}
