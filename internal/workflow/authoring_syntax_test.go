package workflow

import (
	"strings"
	"testing"
)

func TestAllowWritesExpandsToMutationPolicy(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: allow-writes}
spec:
  workspace:
    allowWrites: [src/**, tests/**]
  agents: {worker: {runner: codex}}
  validation:
    gate:
      run: "true"
  phases:
    - {id: build, kind: implementation, actor: worker, validation: gate, prompt: build}
  flow: [{phase: build}]
`))
	if err != nil {
		t.Fatal(err)
	}
	got := d.Workflow.Spec.Workspace.MutationPolicy.Allowed
	if len(got) != 2 || got[0] != "src/**" || got[1] != "tests/**" {
		t.Fatalf("allowed = %#v", got)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
}

func TestAllowWritesRejectsAmbiguousPolicyMerge(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: conflicting-writes}
spec:
  workspace:
    allowWrites: [src/**]
    mutationPolicy:
      allowed: [tests/**]
`))
	if err == nil || !strings.Contains(err.Error(), "both allowWrites and mutationPolicy.allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestInlinePhaseActorExpandsToNamedAgentAndUsesDefaults(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor}
spec:
  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: true
  agents:
    reviewer:
      model: review-model
  validation:
    gate:
      run: "true"
  phases:
    - id: build
      kind: implementation
      actor:
        model: build-model
        may_commit: false
      validation: gate
      prompt: build
    - id: review
      kind: audit
      actor: reviewer
      validation: gate
      prompt: review
  flow:
    - phase: build
    - phase: review
`))
	if err != nil {
		t.Fatal(err)
	}

	actorRef := d.Workflow.Spec.Phases[0].Actor
	if actorRef != "__inline_actor__build" {
		t.Fatalf("inline actor ref = %q", actorRef)
	}
	if d.Workflow.Spec.Phases[1].Actor != "reviewer" {
		t.Fatalf("named actor ref = %q", d.Workflow.Spec.Phases[1].Actor)
	}
	inline, ok := d.Workflow.Spec.Agents[actorRef]
	if !ok || inline.Model != "build-model" || !inline.present["may_commit"] {
		t.Fatalf("inline agent = %#v", inline)
	}
	if _, ok := d.Workflow.Spec.Agents["reviewer"]; !ok {
		t.Fatal("named reviewer agent was removed")
	}

	result := Validate(d)
	if result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	normalizedInline := result.Normalized.Workflow.Spec.Agents[actorRef]
	if normalizedInline.Runner != "codex" || normalizedInline.Model != "build-model" || normalizedInline.Sandbox != "workspace-write" || normalizedInline.Approval != "never" || !normalizedInline.Ephemeral || normalizedInline.MayCommit {
		t.Fatalf("normalized inline agent = %#v", normalizedInline)
	}
	normalizedReviewer := result.Normalized.Workflow.Spec.Agents["reviewer"]
	if normalizedReviewer.Runner != "codex" || normalizedReviewer.Model != "review-model" || !normalizedReviewer.MayCommit {
		t.Fatalf("normalized named agent = %#v", normalizedReviewer)
	}
}

func TestInlinePhaseActorRejectsGeneratedNameCollision(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor-collision}
spec:
  agents:
    __inline_actor__build: {runner: codex}
  phases:
    - id: build
      kind: implementation
      actor: {model: build-model}
      prompt: build
`))
	if err == nil || !strings.Contains(err.Error(), "conflicts with generated agent name") {
		t.Fatalf("err = %v", err)
	}
}

func TestInlinePhaseActorKeepsAgentFieldsStrict(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor-strict}
spec:
  phases:
    - id: build
      kind: implementation
      actor:
        model: build-model
        teleport: true
      prompt: build
`))
	if err == nil || !strings.Contains(err.Error(), "field teleport not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationRunExpandsToShellTool(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-run}
spec:
  agents: {worker: {runner: codex}}
  validation:
    tests:
      run: "go test ./..."
  phases:
    - {id: build, kind: implementation, actor: worker, validation: tests, prompt: build}
  flow: [{phase: build}]
`))
	if err != nil {
		t.Fatal(err)
	}
	validation := d.Workflow.Spec.Validation["tests"]
	if len(validation.Steps) != 1 {
		t.Fatalf("steps = %#v", validation.Steps)
	}
	toolName := validation.Steps[0].Uses
	tool, ok := d.Workflow.Spec.Tools[toolName]
	if !ok {
		t.Fatalf("generated tool %q missing", toolName)
	}
	if tool.Type != "shell" || tool.Command != "go test ./..." {
		t.Fatalf("tool = %#v", tool)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
}

func TestValidationRunPreservesRepairSemantics(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-repair}
spec:
  defaults:
    repair: {actor: worker, reasoning: high, prompt: repair the failure}
  agents: {worker: {runner: codex}}
  validation:
    tests:
      run: "go test ./..."
      repair: once
  phases:
    - {id: build, kind: implementation, actor: worker, validation: tests, prompt: build}
  flow: [{phase: build}]
`))
	if err != nil {
		t.Fatal(err)
	}
	result := Validate(d)
	if result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	v := result.Normalized.Workflow.Spec.Validation["tests"]
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts != 1 || v.OnFailure.Repair.Actor != "worker" {
		t.Fatalf("repair = %#v", v.OnFailure)
	}
	if len(v.Steps) != 1 || d.Workflow.Spec.Tools[v.Steps[0].Uses].Type != "shell" {
		t.Fatalf("validation = %#v", v)
	}
}

func TestValidationRunRejectsStepsCombination(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: conflicting-validation}
spec:
  tools: {gate: {type: shell, command: "true"}}
  validation:
    gate:
      run: "true"
      steps: [{uses: gate}]
`))
	if err == nil || !strings.Contains(err.Error(), "both run and steps") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationRunRejectsGeneratedToolCollision(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: tool-collision}
spec:
  tools:
    __inline_validation__gate: {type: shell, command: "false"}
  validation:
    gate:
      run: "true"
`))
	if err == nil || !strings.Contains(err.Error(), "conflicts with generated tool name") {
		t.Fatalf("err = %v", err)
	}
}

func TestConciseSpecUnmarshalKeepsKnownFieldsStrict(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: strict-fields}
spec:
  allowMagic: true
`))
	if err == nil || !strings.Contains(err.Error(), "field allowMagic not found") {
		t.Fatalf("err = %v", err)
	}
}
