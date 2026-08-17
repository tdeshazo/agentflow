package workflow

import "testing"

const conciseFixture = `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata:
  name: concise
spec:
  defaults:
    agent:
      runner: codex
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: true
      output_last_message: true
    lifecycle:
      policy: safe-resume
      validation: gate
    phases:
      implementation:
        actor: worker
        reasoning: high
        requiresChange: true
    repair:
      actor: worker
      reasoning: medium
      prompt: repair the bounded validation failure
  agents:
    worker:
      model: gpt-5.6-terra
      may_commit: false
  tools:
    gate:
      type: shell
      command: "true"
  validation:
    gate:
      repair: once
      steps:
        - uses: gate
  phases:
    - id: build
      kind: implementation
      prompt: make the bounded change
  flow:
    - phase: build
`

func TestNormalizeWorkflowResolvesConciseDefaults(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("authored status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	} else if result.Normalized == nil || result.Normalized.Workflow.Spec.Phases[0].Actor != "worker" {
		t.Fatalf("normalized result = %#v", result.Normalized)
	}
	n, err := NormalizeWorkflow(d)
	if err != nil {
		t.Fatal(err)
	}
	a := n.Workflow.Spec.Agents["worker"]
	if a.Runner != "codex" || a.Sandbox != "workspace-write" || !a.Ephemeral || a.MayCommit || !a.OutputLastMessage {
		t.Fatalf("resolved agent = %#v", a)
	}
	p := n.Workflow.Spec.Phases[0]
	if p.Actor != "worker" || p.Reasoning != "high" || !p.RequiresChange || p.Validation != "gate" {
		t.Fatalf("resolved phase = %#v", p)
	}
	if n.Workflow.Spec.Lifecycle.Validation != "gate" {
		t.Fatalf("lifecycle = %#v", n.Workflow.Spec.Lifecycle)
	}
	v := n.Workflow.Spec.Validation["gate"]
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts != 1 || v.OnFailure.Repair.Actor != "worker" || len(v.OnFailure.Then) != 0 {
		t.Fatalf("repair = %#v", v.OnFailure)
	}
}

func TestBuildExpandedPlanExposesRuntimeContract(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ResolvedLifecycle.Policy != "safe-resume" || len(plan.SafetyEnforcementPoints) < 4 || len(plan.RecoveryBehavior) < 3 {
		t.Fatalf("plan = %#v", plan)
	}
	if len(plan.Validations) != 1 || plan.Validations[0].Repair != "one repair actor attempt; rerun the same deterministic validation steps" {
		t.Fatalf("validations = %#v", plan.Validations)
	}
}

func TestConciseAuthoringNormalizesToEquivalentLegacyContract(t *testing.T) {
	concise, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: concise}
spec:
  agents:
    worker:
      runner: codex
      model: gpt-5.6-terra
      sandbox: workspace-write
      approval: never
      ephemeral: true
      may_commit: false
      output_last_message: true
  tools: {gate: {type: shell, command: "true"}}
  validation:
    gate:
      steps: [{uses: gate}]
      onFailure:
        strategy: repair-once
        maxRepairAttempts: 1
        repair: {actor: worker, reasoning: medium, prompt: repair the bounded validation failure}
  lifecycle: {policy: safe-resume, validation: gate}
  phases:
    - {id: build, kind: implementation, actor: worker, reasoning: high, requiresChange: true, validation: gate, prompt: make the bounded change}
  flow: [{phase: build}]
`
	verbose, err := Decode(writeWorkflow(t, legacy))
	if err != nil {
		t.Fatal(err)
	}
	cn, err := NormalizeWorkflow(concise)
	if err != nil {
		t.Fatal(err)
	}
	vn, err := NormalizeWorkflow(verbose)
	if err != nil {
		t.Fatal(err)
	}
	ca, va := cn.Workflow.Spec.Agents["worker"], vn.Workflow.Spec.Agents["worker"]
	if ca.Runner != va.Runner || ca.Sandbox != va.Sandbox || ca.Approval != va.Approval || ca.Ephemeral != va.Ephemeral || ca.MayCommit != va.MayCommit {
		t.Fatalf("agents differ: %#v != %#v", ca, va)
	}
	cp, vp := cn.Workflow.Spec.Phases[0], vn.Workflow.Spec.Phases[0]
	if cp.Actor != vp.Actor || cp.Reasoning != vp.Reasoning || cp.RequiresChange != vp.RequiresChange || cp.Validation != vp.Validation {
		t.Fatalf("phases differ: %#v != %#v", cp, vp)
	}
	cr, vr := cn.Workflow.Spec.Validation["gate"].OnFailure, vn.Workflow.Spec.Validation["gate"].OnFailure
	if cn.Workflow.Spec.Lifecycle != vn.Workflow.Spec.Lifecycle || cr.Strategy != vr.Strategy || cr.MaxRepairAttempts != vr.MaxRepairAttempts || cr.Repair != vr.Repair {
		t.Fatal("lifecycle or repair contract differs")
	}
}
