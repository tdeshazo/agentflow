package workflow

import (
	"strings"
	"testing"
)

func TestExpandedPlanRevealsResolvedDefaultsAndAcceptanceOrder(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ResolvedAgents) != 1 || plan.ResolvedAgents[0].Runner != "codex" || plan.ResolvedAgents[0].Sandbox != "workspace-write" || plan.ResolvedAgents[0].MayCommit {
		t.Fatalf("resolved agents = %#v", plan.ResolvedAgents)
	}
	if plan.ResolvedLifecycle.Policy != "safe-resume" || plan.ResolvedLifecycle.Validation != "gate" {
		t.Fatalf("resolved lifecycle = %#v", plan.ResolvedLifecycle)
	}
	if got := strings.Join(plan.SafetyEnforcementPoints, "|"); !strings.Contains(got, "including provider errors") || !strings.Contains(got, "may_commit for the invoked actor") {
		t.Fatalf("expanded safety enforcement points = %q", got)
	}
	if !strings.Contains(plan.CheckpointBehavior, "runtime-owned") || !strings.Contains(plan.CheckpointBehavior, "not an actor may_commit exercise") {
		t.Fatalf("expanded checkpoint behavior = %q", plan.CheckpointBehavior)
	}
	if len(plan.Validations) != 1 || plan.Validations[0].RepairActor != "worker" || plan.Validations[0].RepairReasoning != "medium" {
		t.Fatalf("resolved validation repair = %#v", plan.Validations)
	}
	if len(plan.Phases) != 1 {
		t.Fatalf("resolved phases = %#v", plan.Phases)
	}
	acceptance := strings.Join(plan.Phases[0].Acceptance, "|")
	for _, want := range []string{"run actor", "persist actor_completed", "deterministic validation", "checkpoint", "write completed commit marker"} {
		if !strings.Contains(acceptance, want) {
			t.Fatalf("phase acceptance %q missing %q", acceptance, want)
		}
	}
	if strings.Index(acceptance, "deterministic validation") > strings.Index(acceptance, "checkpoint") {
		t.Fatalf("validation occurs after checkpoint: %q", acceptance)
	}
}
