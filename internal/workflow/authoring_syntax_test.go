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

func TestInlineActorPrefixReservedForNamedAgents(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor-reserved}
spec:
  agents:
    __inline_actor__build: {runner: codex}
`))
	if err == nil || !strings.Contains(err.Error(), "uses reserved prefix") {
		t.Fatalf("err = %v", err)
	}
}

func TestInlineActorPrefixCannotBeReferencedByPhase(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor-reference}
spec:
  phases:
    - id: review
      kind: audit
      actor: __inline_actor__build
      prompt: review
`))
	if err == nil || !strings.Contains(err.Error(), "references reserved inline actor name") {
		t.Fatalf("err = %v", err)
	}
}

func TestInlineActorPrefixCannotBeReferencedByRepair(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: inline-actor-repair-reference}
spec:
  defaults:
    repair:
      actor: __inline_actor__build
`))
	if err == nil || !strings.Contains(err.Error(), "references reserved inline actor name") {
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

func TestValidationRunRejectsMergeKey(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: merged-validation}
spec:
  tools:
    existing-check: {type: shell, command: "true"}
  validation:
    shared: &shared
      steps:
        - uses: existing-check
    gate:
      <<: *shared
      run: go test ./...
`))
	if err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported in validation.gate") {
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

func TestFoldedScalarsPreservedWithAndWithoutConciseSyntax(t *testing.T) {
	canonical, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: folded-canonical}
spec:
  workspace:
    mutationPolicy:
      allowed: [src/**]
  defaults:
    repair:
      actor: repairer
      reasoning: high
      prompt: >-
        Repair the first issue
        and keep the second constraint.
  agents:
    repairer: {runner: codex}
    worker: {runner: codex}
  tools:
    gate:
      type: shell
      command: >-
        printf one &&
        printf two
  validation:
    gate:
      repair: once
      steps: [{uses: gate}]
  phases:
    - id: build
      kind: implementation
      actor: worker
      validation: gate
      prompt: >-
        Review the first line
        and the second line.
  flow: [{phase: build}]
`))
	if err != nil {
		t.Fatal(err)
	}

	concise, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: folded-concise}
spec:
  workspace:
    allowWrites: [src/**]
  defaults:
    agent:
      runner: codex
    repair:
      actor: repairer
      reasoning: high
      prompt: >-
        Repair the first issue
        and keep the second constraint.
  agents:
    repairer: {model: repair-model}
  validation:
    gate:
      run: >-
        printf one &&
        printf two
      repair: once
  phases:
    - id: build
      kind: implementation
      actor:
        model: build-model
      validation: gate
      prompt: >-
        Review the first line
        and the second line.
  flow: [{phase: build}]
`))
	if err != nil {
		t.Fatal(err)
	}

	const wantPrompt = "Review the first line and the second line."
	const wantRepair = "Repair the first issue and keep the second constraint."
	const wantCommand = "printf one && printf two"
	if got := canonical.Workflow.Spec.Phases[0].Prompt; got != wantPrompt {
		t.Fatalf("canonical prompt = %q, want %q", got, wantPrompt)
	}
	if got := concise.Workflow.Spec.Phases[0].Prompt; got != wantPrompt {
		t.Fatalf("concise prompt = %q, want %q", got, wantPrompt)
	}
	if got := canonical.Workflow.Spec.Defaults.Repair.Prompt; got != wantRepair {
		t.Fatalf("canonical repair prompt = %q, want %q", got, wantRepair)
	}
	if got := concise.Workflow.Spec.Defaults.Repair.Prompt; got != wantRepair {
		t.Fatalf("concise repair prompt = %q, want %q", got, wantRepair)
	}
	if got := canonical.Workflow.Spec.Tools["gate"].Command; got != wantCommand {
		t.Fatalf("canonical command = %q, want %q", got, wantCommand)
	}
	steps := concise.Workflow.Spec.Validation["gate"].Steps
	if len(steps) != 1 {
		t.Fatalf("concise validation steps = %#v", steps)
	}
	if got := concise.Workflow.Spec.Tools[steps[0].Uses].Command; got != wantCommand {
		t.Fatalf("concise command = %q, want %q", got, wantCommand)
	}
}

func TestConcisePreprocessingKeepsKnownFieldsStrict(t *testing.T) {
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
