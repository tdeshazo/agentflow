package workflow

import (
	"fmt"
	"path/filepath"
	"reflect"
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
	if len(plan.ContextRecipes) != 3 {
		t.Fatalf("context recipes = %#v", plan.ContextRecipes)
	}
	for _, recipe := range plan.ContextRecipes {
		if len(recipe.Included) == 0 || len(recipe.Excluded) == 0 {
			t.Fatalf("incomplete context recipe = %#v", recipe)
		}
		for _, component := range recipe.Included {
			if component.Component == "workspace state" && !component.RuntimeResolved {
				t.Fatalf("workspace recipe is not runtime-resolved: %#v", recipe)
			}
		}
	}
	repairRecipe := plan.ContextRecipes[2]
	if repairRecipe.Role != "validation-repair" || repairRecipe.Validation != "gate" || repairRecipe.Included[len(repairRecipe.Included)-1].Component != "selected repair failure" {
		t.Fatalf("repair context recipe = %#v", repairRecipe)
	}
}

func TestExpandedPlansExposeContextRecipesAcrossAPIVersions(t *testing.T) {
	paths := []string{
		filepath.Join("testdata", "conformance", "valid", "minimal.yaml"),
		filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml"),
		filepath.Join("testdata", "conformance", "valid", "v1alpha3-typed-contracts.yaml"),
		filepath.Join("testdata", "conformance", "valid", "v1alpha4-typed-work-items.yaml"),
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			document, err := Decode(path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildExpandedPlan(document)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.ContextRecipes) == 0 {
				t.Fatalf("%s has no context recipes", document.Workflow.APIVersion)
			}
			for _, recipe := range plan.ContextRecipes {
				blob := fmt.Sprintf("%#v", recipe)
				if strings.Contains(blob, document.Workflow.Spec.Phases[0].Prompt) {
					t.Fatalf("context recipe exposed prompt text: %s", blob)
				}
			}
		})
	}
}

func TestExpandedPlanIncludesCompleteNormalizedExecutionContract(t *testing.T) {
	d, err := Decode(filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeWorkflow(d)
	if err != nil {
		t.Fatal(err)
	}
	want := *normalized.Workflow
	want.Spec = normalized.Workflow.Spec
	want.Spec.Defaults = AuthoringDefaults{}
	want.Spec.Phases = append([]Phase(nil), normalized.Workflow.Spec.Phases...)
	for i := range want.Spec.Phases {
		want.Spec.Phases[i].Prompt = ""
	}
	want.Spec.Validation = make(map[string]Validation, len(normalized.Workflow.Spec.Validation))
	for name, validation := range normalized.Workflow.Spec.Validation {
		validation.OnFailure.Repair.Prompt = ""
		want.Spec.Validation[name] = validation
	}
	want.DependencyGraph = clonePhaseDependencyGraph(normalized.DependencyGraph)
	if !reflect.DeepEqual(plan.NormalizedExecution, want) {
		t.Fatalf("normalized execution differs from executable workflow:\n got %#v\nwant %#v", plan.NormalizedExecution, want)
	}
	if len(plan.NormalizedExecution.Spec.Preconditions) == 0 ||
		len(plan.NormalizedExecution.Spec.Workspace.MutationPolicy.Integrity) == 0 ||
		len(plan.NormalizedExecution.Spec.Flow) == 0 ||
		len(plan.NormalizedExecution.Spec.Completion) == 0 {
		t.Fatalf("complete execution contract omitted stateful policy: %#v", plan.NormalizedExecution.Spec)
	}
}

func TestExpandedPlanMaterializesConciseDefaultsWithoutRetainingAuthoringDefaults(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.NormalizedExecution.Spec.Defaults, AuthoringDefaults{}) {
		t.Fatalf("normalized execution retained authoring defaults: %#v", plan.NormalizedExecution.Spec.Defaults)
	}
	actor := plan.NormalizedExecution.Spec.Agents["worker"]
	if actor.Runner != "codex" || actor.Sandbox != "workspace-write" || actor.MayCommit {
		t.Fatalf("normalized execution hid resolved actor authority: %#v", actor)
	}
	phase := plan.NormalizedExecution.Spec.Phases[0]
	if phase.Validation != "gate" || !phase.RequiresChange || phase.Actor != "worker" {
		t.Fatalf("normalized execution hid resolved phase authority: %#v", phase)
	}
	repair := plan.NormalizedExecution.Spec.Validation["gate"].OnFailure
	if repair.Strategy != "repair-once" || repair.MaxRepairAttempts != 1 || repair.Repair.Actor != "worker" {
		t.Fatalf("normalized execution hid resolved repair authority: %#v", repair)
	}
}
