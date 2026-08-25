package workflow

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const v1alpha2Fixture = `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata:
  name: v1alpha2-fixture
spec:
  workspace:
    allowWrites: [src/**, tests/**]
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
    reviewer: {runner: codex, model: gpt-5.6-luna}
  validation:
    tests:
      run: go test ./...
      repair: {once: coder}
  phases:
    - {id: implement, actor: coder, prompt: Implement the feature., validation: tests}
    - {id: review, actor: reviewer, prompt: Review the feature., validation: tests, dependsOn: [implement]}
  completion: {validation: tests}
`

func TestDecodeDispatchesV1Alpha2AndNormalizesDependencies(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1alpha2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if d.V1Alpha2 == nil || d.Workflow == nil || d.Workflow.APIVersion != v1alpha2APIVersion {
		t.Fatalf("decoded document = %#v", d)
	}
	if got := d.Workflow.Spec.Workspace.MutationPolicy.Allowed; len(got) != 2 || got[0] != "src/**" || got[1] != "tests/**" {
		t.Fatalf("normalized allowlist = %#v", got)
	}
	if got := d.PhaseDependencies["review"]; len(got) != 1 || got[0] != "implement" {
		t.Fatalf("dependencies = %#v", d.PhaseDependencies)
	}
	result := Validate(d)
	if result.Status != Executable || result.Normalized == nil {
		t.Fatalf("status = %s, normalized = %#v, diagnostics = %#v", result.Status, result.Normalized, result.Diagnostics)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Phases) != 2 || len(plan.Phases[1].DependsOn) != 1 || plan.Phases[1].DependsOn[0] != "implement" {
		t.Fatalf("planned phases = %#v", plan.Phases)
	}
}

func TestV1Alpha2ExpandedPlanExposesEffectiveActorCapabilities(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture,
		"coder: {runner: codex, model: gpt-5.6-terra}",
		"coder: {runner: codex, model: gpt-5.6-terra, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true}",
		1,
	)
	d, err := Decode(writeWorkflow(t, document))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildExpandedPlan(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ResolvedAgents) != 2 {
		t.Fatalf("planned agents = %#v", plan.ResolvedAgents)
	}
	coder := plan.ResolvedAgents[0]
	if coder.Name != "coder" || coder.Runner != "codex" || coder.Model != "gpt-5.6-terra" ||
		coder.Sandbox != "workspace-write" || coder.Approval != "never" || !coder.Ephemeral ||
		!coder.MayCommit || !coder.OutputLastMessage {
		t.Fatalf("planned coder capabilities = %#v", coder)
	}
}

func TestV1Alpha2ValidationReportsUnsupportedAgentCapabilities(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		path   string
		inline bool
	}{
		{name: "named unsupported approval", field: "runner: codex, model: capability-model, approval: on-request", path: "spec.agents.coder.approval"},
		{name: "inline unsupported approval", field: "runner: codex, model: capability-model, approval: on-request", path: "spec.phases[0].actor.approval", inline: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configuration := tt.field
			if !strings.Contains(configuration, "model:") {
				configuration += ", model: capability-model"
			}
			result := ValidateFile(writeWorkflow(t, v1alpha2AgentDocument(configuration, tt.inline)))
			if result.Status != Unsupported {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, Unsupported, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Path == tt.path && strings.Contains(diagnostic.Message, "not implemented") {
					return
				}
			}
			t.Fatalf("diagnostics = %#v, want unsupported path %q", result.Diagnostics, tt.path)
		})
	}
}

func TestV1Alpha2ExpandedPlanRejectsUnsupportedAgentCapabilities(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1alpha2AgentDocument("runner: codex, model: capability-model, approval: on-request", false)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildExpandedPlan(d); err == nil || !strings.Contains(err.Error(), "unsupported by this runtime") {
		t.Fatalf("expanded plan error = %v", err)
	}
}

func TestV1Alpha2PhaseActorForms(t *testing.T) {
	t.Run("named scalar actor", func(t *testing.T) {
		d, err := Decode(writeWorkflow(t, v1alpha2Fixture))
		if err != nil {
			t.Fatal(err)
		}
		if got := d.V1Alpha2.Spec.Phases[0].Actor; got.Name != "coder" || got.Inline != nil {
			t.Fatalf("authored actor = %#v", got)
		}
		if got := d.Workflow.Spec.Phases[0].Actor; got != "coder" {
			t.Fatalf("normalized actor = %q, want coder", got)
		}
	})

	t.Run("inline mapping actor", func(t *testing.T) {
		document := strings.Replace(v1alpha2Fixture, "actor: coder", "actor: {runner: codex, model: gpt-5.6-terra}", 1)
		d, err := Decode(writeWorkflow(t, document))
		if err != nil {
			t.Fatal(err)
		}
		inline := d.V1Alpha2.Spec.Phases[0].Actor.Inline
		if inline == nil || inline.Runner != "codex" || inline.Model != "gpt-5.6-terra" {
			t.Fatalf("authored inline actor = %#v", inline)
		}
		const generated = "__inline_actor__implement"
		if got := d.Workflow.Spec.Phases[0].Actor; got != generated {
			t.Fatalf("normalized actor = %q, want %q", got, generated)
		}
		if got := d.Workflow.Spec.Agents[generated]; got.Runner != "codex" || got.Model != "gpt-5.6-terra" {
			t.Fatalf("generated agent = %#v", got)
		}
		if got := d.Workflow.Spec.Phases[1].Actor; got != "reviewer" {
			t.Fatalf("coexisting named actor = %q, want reviewer", got)
		}
		if result := Validate(d); result.Status != Executable {
			t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
		}
	})
}

func TestV1Alpha2InlineActorKeepsAgentFieldsStrict(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture, "actor: coder", "actor: {runner: codex, model: gpt-5.6-terra, teleport: true}", 1)
	_, err := Decode(writeWorkflow(t, document))
	if err == nil || !strings.Contains(err.Error(), "field teleport not found") {
		t.Fatalf("strict decode error = %v", err)
	}
}

func TestV1Alpha2AgentFieldsNormalizeIndependently(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  Agent
	}{
		{name: "sandbox", field: "sandbox: workspace-write", want: Agent{Sandbox: "workspace-write"}},
		{name: "approval", field: "approval: never", want: Agent{Approval: "never"}},
		{name: "ephemeral true", field: "ephemeral: true", want: Agent{Ephemeral: true}},
		{name: "ephemeral false", field: "ephemeral: false", want: Agent{}},
		{name: "may_commit true", field: "may_commit: true", want: Agent{MayCommit: true}},
		{name: "may_commit false", field: "may_commit: false", want: Agent{}},
		{name: "output_last_message true", field: "output_last_message: true", want: Agent{OutputLastMessage: true}},
		{name: "output_last_message false", field: "output_last_message: false", want: Agent{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, inline := range []bool{false, true} {
				name := "named"
				if inline {
					name = "inline"
				}
				t.Run(name, func(t *testing.T) {
					got := decodeV1Alpha2Agent(t, "runner: codex, model: gpt-5.6-terra, "+tt.field, inline)
					want := tt.want
					want.Runner = "codex"
					want.Model = "gpt-5.6-terra"
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("normalized agent = %#v, want %#v", got, want)
					}
				})
			}
		})
	}
}

func TestV1Alpha2AgentFieldsNormalizeTogether(t *testing.T) {
	tests := []struct {
		name          string
		configuration string
		want          Agent
	}{
		{
			name:          "enabled booleans",
			configuration: "runner: codex, model: gpt-5.6-terra, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true",
			want: Agent{
				Runner: "codex", Model: "gpt-5.6-terra", Sandbox: "workspace-write", Approval: "never",
				Ephemeral: true, MayCommit: true, OutputLastMessage: true,
			},
		},
		{
			name:          "disabled booleans",
			configuration: "runner: codex, model: gpt-5.6-terra, sandbox: workspace-write, approval: never, ephemeral: false, may_commit: false, output_last_message: false",
			want:          Agent{Runner: "codex", Model: "gpt-5.6-terra", Sandbox: "workspace-write", Approval: "never"},
		},
		{
			name:          "omitted fields",
			configuration: "runner: codex, model: gpt-5.6-terra",
			want:          Agent{Runner: "codex", Model: "gpt-5.6-terra"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, inline := range []bool{false, true} {
				name := "named"
				if inline {
					name = "inline"
				}
				t.Run(name, func(t *testing.T) {
					if got := decodeV1Alpha2Agent(t, tt.configuration, inline); !reflect.DeepEqual(got, tt.want) {
						t.Fatalf("normalized agent = %#v, want %#v", got, tt.want)
					}
				})
			}
		})
	}
}

func TestV1Alpha2ExplicitFalseCapabilityFieldsSurviveNormalization(t *testing.T) {
	configuration := "runner: codex, model: capability-model, sandbox: workspace-write, approval: never, ephemeral: false, may_commit: false, output_last_message: false"
	want := Agent{
		Runner: "codex", Model: "capability-model", Sandbox: "workspace-write", Approval: "never",
	}

	for _, test := range []struct {
		name       string
		inline     bool
		agentName  string
		actorCheck func(*V1Alpha2Workflow) V1Alpha2Agent
	}{
		{
			name:      "named actor",
			agentName: "coder",
			actorCheck: func(w *V1Alpha2Workflow) V1Alpha2Agent {
				return w.Spec.Agents["coder"]
			},
		},
		{
			name:      "inline actor",
			inline:    true,
			agentName: "__inline_actor__implement",
			actorCheck: func(w *V1Alpha2Workflow) V1Alpha2Agent {
				return *w.Spec.Phases[0].Actor.Inline
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, err := Decode(writeWorkflow(t, v1alpha2AgentDocument(configuration, test.inline)))
			if err != nil {
				t.Fatal(err)
			}
			authored := test.actorCheck(d.V1Alpha2)
			if authored.Ephemeral || authored.MayCommit || authored.OutputLastMessage {
				t.Fatalf("authored explicit false capabilities = %#v", authored)
			}
			if got := d.Workflow.Spec.Agents[test.agentName]; !reflect.DeepEqual(got, want) {
				t.Fatalf("normalized explicit false capabilities = %#v, want %#v", got, want)
			}
		})
	}
}

func TestV1Alpha2AgentFieldsRejectWrongYAMLTypes(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{name: "sandbox", field: "sandbox: true"},
		{name: "approval", field: "approval: true"},
		{name: "ephemeral", field: "ephemeral: 'true'"},
		{name: "may_commit", field: "may_commit: 'true'"},
		{name: "output_last_message", field: "output_last_message: 'true'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, inline := range []bool{false, true} {
				name := "named"
				if inline {
					name = "inline"
				}
				t.Run(name, func(t *testing.T) {
					configuration := "runner: codex, model: gpt-5.6-terra, " + tt.field
					document := v1alpha2AgentDocument(configuration, inline)
					if _, err := Decode(writeWorkflow(t, document)); err == nil {
						t.Fatal("Decode succeeded for a malformed agent field")
					}
				})
			}
		})
	}
}

func TestV1Alpha2ActorFormsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name  string
		actor string
	}{
		{name: "non-string scalar", actor: "true"},
		{name: "sequence", actor: "[coder]"},
		{name: "mapping with invalid field type", actor: "{runner: true, model: capability-model}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := strings.Replace(v1alpha2Fixture, "actor: coder", "actor: "+test.actor, 1)
			if _, err := Decode(writeWorkflow(t, document)); err == nil {
				t.Fatal("Decode accepted an invalid actor form")
			}
		})
	}
}

func decodeV1Alpha2Agent(t *testing.T, configuration string, inline bool) Agent {
	t.Helper()
	d, err := Decode(writeWorkflow(t, v1alpha2AgentDocument(configuration, inline)))
	if err != nil {
		t.Fatal(err)
	}
	name := "coder"
	if inline {
		name = "__inline_actor__implement"
	}
	return d.Workflow.Spec.Agents[name]
}

func v1alpha2AgentDocument(configuration string, inline bool) string {
	if inline {
		return strings.Replace(v1alpha2Fixture, "actor: coder", "actor: {"+configuration+"}", 1)
	}
	return strings.Replace(v1alpha2Fixture, "coder: {runner: codex, model: gpt-5.6-terra}", "coder: {"+configuration+"}", 1)
}

func TestV1Alpha2InlineActorNamespaceIsRuntimeOwned(t *testing.T) {
	tests := []struct {
		name   string
		modify func(string) string
		want   string
	}{
		{
			name: "reserved authored agent name",
			modify: func(document string) string {
				return strings.Replace(document, "coder: {runner:", "__inline_actor__implement: {runner:", 1)
			},
			want: "uses reserved prefix",
		},
		{
			name: "reserved scalar phase actor reference",
			modify: func(document string) string {
				return strings.Replace(document, "actor: coder", "actor: __inline_actor__implement", 1)
			},
			want: "phases[0].actor references reserved inline actor name",
		},
		{
			name: "reserved repair once actor reference",
			modify: func(document string) string {
				return strings.Replace(document, "repair: {once: coder}", "repair: {once: __inline_actor__implement}", 1)
			},
			want: "validation.tests.repair.once references reserved inline actor name",
		},
		{
			name: "aliased reserved phase actor reference",
			modify: func(document string) string {
				document = strings.Replace(document, "name: v1alpha2-fixture", "name: &reserved __inline_actor__implement", 1)
				return strings.Replace(document, "actor: coder", "actor: *reserved", 1)
			},
			want: "phases[0].actor references reserved inline actor name",
		},
		{
			name: "aliased reserved repair actor reference",
			modify: func(document string) string {
				document = strings.Replace(document, "name: v1alpha2-fixture", "name: &reserved __inline_actor__implement", 1)
				return strings.Replace(document, "repair: {once: coder}", "repair: {once: *reserved}", 1)
			},
			want: "validation.tests.repair.once references reserved inline actor name",
		},
		{
			name: "aliased reserved authored agent name",
			modify: func(document string) string {
				document = strings.Replace(document, "name: v1alpha2-fixture", "name: &reserved __inline_actor__implement", 1)
				return strings.Replace(document, "coder: {runner:", "*reserved: {runner:", 1)
			},
			want: "uses reserved prefix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(writeWorkflow(t, tt.modify(v1alpha2Fixture)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("namespace error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestV1Alpha2InlineActorGeneratedNameCollisionFailsClosed(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture,
		"- {id: implement, actor: coder, prompt: Implement the feature., validation: tests}",
		"- {id: implement, actor: {runner: codex, model: gpt-5.6-terra}, prompt: Implement the feature., validation: tests}\n    - {id: implement, actor: {runner: codex, model: gpt-5.6-luna}, prompt: Implement it again., validation: tests}", 1)
	_, err := Decode(writeWorkflow(t, document))
	if err == nil || !strings.Contains(err.Error(), `conflicts with generated agent name "__inline_actor__implement"`) {
		t.Fatalf("collision error = %v", err)
	}
}

func TestV1Alpha2InlineActorPreservesPhaseDependencies(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture, "actor: reviewer", "actor: {runner: codex, model: gpt-5.6-luna}", 1)
	d, err := Decode(writeWorkflow(t, document))
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Workflow.Spec.Phases[1].Actor; got != "__inline_actor__review" {
		t.Fatalf("normalized review actor = %q", got)
	}
	edges := d.DependencyGraph.Edges
	if len(edges) != 1 || edges[0].Phase != "review" || edges[0].DependsOn != "implement" || edges[0].SatisfiedWhen != PhaseDependencyAccepted {
		t.Fatalf("dependency edges = %#v", edges)
	}
}

func TestV1Alpha2InlineAndNamedActorsNormalizeEquivalently(t *testing.T) {
	configuration := "runner: codex, model: capability-model, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true"
	named, err := Decode(writeWorkflow(t, v1alpha2AgentDocument(configuration, false)))
	if err != nil {
		t.Fatal(err)
	}
	inlineDocument := v1alpha2AgentDocument(configuration, true)
	inline, err := Decode(writeWorkflow(t, inlineDocument))
	if err != nil {
		t.Fatal(err)
	}

	const generated = "__inline_actor__implement"
	if got, want := inline.Workflow.Spec.Agents[generated], named.Workflow.Spec.Agents["coder"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("inline agent = %#v, named agent = %#v", got, want)
	}
	inlinePhase := inline.Workflow.Spec.Phases[0]
	namedPhase := named.Workflow.Spec.Phases[0]
	inlinePhase.Actor = "coder"
	if !reflect.DeepEqual(inlinePhase, namedPhase) {
		t.Fatalf("inline phase = %#v, named phase = %#v", inlinePhase, namedPhase)
	}
}

func TestV1Alpha2ExpandedRepairActorRemainsNamedAndBounded(t *testing.T) {
	configuration := "runner: codex, model: repair-model, sandbox: workspace-write, approval: never, ephemeral: true, may_commit: true, output_last_message: true"
	d, err := Decode(writeWorkflow(t, v1alpha2AgentDocument(configuration, false)))
	if err != nil {
		t.Fatal(err)
	}

	if got := d.Workflow.Spec.Validation["tests"].OnFailure; got.Strategy != "repair-once" || got.MaxRepairAttempts != 1 || got.Repair.Actor != "coder" {
		t.Fatalf("expanded repair policy = %#v, want named coder with one attempt", got)
	}
	if _, ok := d.Workflow.Spec.Agents["__inline_actor__implement"]; ok {
		t.Fatal("named repair actor unexpectedly became an inline actor")
	}
	want := Agent{
		Runner: "codex", Model: "repair-model", Sandbox: "workspace-write", Approval: "never",
		Ephemeral: true, MayCommit: true, OutputLastMessage: true,
	}
	if got := d.Workflow.Spec.Agents["coder"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized repair actor = %#v, want %#v", got, want)
	}
}

func TestConciseAuthoringParityAcrossVersions(t *testing.T) {
	features := []struct {
		name     string
		v1alpha1 string
		v1alpha2 string
	}{
		{
			name: "workspace.allowWrites",
			v1alpha1: `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: parity-allow-writes}
spec: {workspace: {allowWrites: [src/**]}}
`,
			v1alpha2: `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: parity-allow-writes}
spec: {workspace: {allowWrites: [src/**]}}
`,
		},
		{
			name: "validation.<name>.run",
			v1alpha1: `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: parity-validation-run}
spec: {validation: {tests: {run: go test ./...}}}
`,
			v1alpha2: `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: parity-validation-run}
spec: {validation: {tests: {run: go test ./...}}}
`,
		},
		{
			name: "mapping-valued phase.actor",
			v1alpha1: `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: parity-inline-actor}
spec:
  phases:
    - {id: review, kind: audit, actor: {runner: codex, model: gpt-5.6-luna}, prompt: Review.}
`,
			v1alpha2: `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: parity-inline-actor}
spec:
  phases:
    - {id: review, actor: {runner: codex, model: gpt-5.6-luna}, prompt: Review.}
`,
		},
	}
	for _, feature := range features {
		t.Run(feature.name, func(t *testing.T) {
			versions := []struct {
				name     string
				document string
			}{
				{name: "v1alpha1", document: feature.v1alpha1},
				{name: "v1alpha2", document: feature.v1alpha2},
			}
			for _, version := range versions {
				t.Run(version.name, func(t *testing.T) {
					if _, err := Decode(writeWorkflow(t, version.document)); err != nil {
						t.Fatalf("%s does not support %s: %v", version.name, feature.name, err)
					}
				})
			}
		})
	}
}

func TestV1Alpha2RepairNormalizesToBoundedDeterministicValidation(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1alpha2Fixture))
	if err != nil {
		t.Fatal(err)
	}

	v := d.Workflow.Spec.Validation["tests"]
	if v.OnFailure.Strategy != "repair-once" {
		t.Fatalf("repair strategy = %q, want repair-once", v.OnFailure.Strategy)
	}
	if v.OnFailure.MaxRepairAttempts != 1 {
		t.Fatalf("repair attempts = %d, want exactly one", v.OnFailure.MaxRepairAttempts)
	}
	if v.OnFailure.Repair.Actor != "coder" {
		t.Fatalf("repair actor = %q, want coder", v.OnFailure.Repair.Actor)
	}
	prompt := v.OnFailure.Repair.Prompt
	if strings.TrimSpace(prompt) == "" || !strings.Contains(prompt, "{{ validation.failure.log }}") {
		t.Fatalf("repair prompt = %q, want non-empty prompt containing validation failure log", prompt)
	}
	if len(v.Steps) != 1 || v.Steps[0].Uses != v1Alpha2ValidationToolName("tests") {
		t.Fatalf("deterministic validation steps = %#v", v.Steps)
	}
	if len(v.OnFailure.Then) != 0 {
		t.Fatalf("unexpected alternate post-repair steps = %#v", v.OnFailure.Then)
	}
}

func TestV1Alpha2ConformanceExampleRemainsStrictlyDecoded(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "valid", "v1alpha2-concise.yaml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(body), "      run: make test", "      run: make test\n      unknowable: true", 1)
	_, err = Decode(writeWorkflow(t, unknown))
	if err == nil || !strings.Contains(err.Error(), "field unknowable not found") {
		t.Fatalf("strict decode error = %v", err)
	}
}

func TestDecodeKeepsV1Alpha1Behavior(t *testing.T) {
	d, err := Decode(writeWorkflow(t, executableFixture))
	if err != nil {
		t.Fatal(err)
	}
	if d.V1Alpha2 != nil || d.Workflow.APIVersion != v1alpha1APIVersion {
		t.Fatalf("decoded document = %#v", d)
	}
	if result := Validate(d); result.Status != Executable {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	_, err = Decode(writeWorkflow(t, strings.Replace(executableFixture, "prompt: make the bounded change", "prompt: make the bounded change\n      dependsOn: [other]", 1)))
	if err == nil || !strings.Contains(err.Error(), "field dependsOn not found") {
		t.Fatalf("v1alpha1 dependsOn error = %v", err)
	}
}

func TestDecodeV1Alpha2KeepsKnownFieldsStrict(t *testing.T) {
	document := strings.Replace(v1alpha2Fixture, "runner: codex, model: gpt-5.6-terra", "runner: codex, model: gpt-5.6-terra, teleport: true", 1)
	_, err := Decode(writeWorkflow(t, document))
	if err == nil || !strings.Contains(err.Error(), "field teleport not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodeRejectsUnknownAPIVersion(t *testing.T) {
	_, err := Decode(writeWorkflow(t, strings.Replace(v1alpha2Fixture, v1alpha2APIVersion, "agentflow.dev/v9", 1)))
	if err == nil || !strings.Contains(err.Error(), `unsupported apiVersion "agentflow.dev/v9"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestV1Alpha2ReferencesFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		replace  string
		with     string
		path     string
		contains string
	}{
		{
			name:     "unknown phase dependency",
			replace:  "dependsOn: [implement]",
			with:     "dependsOn: [missing]",
			path:     "spec.phases[1].dependsOn[0]",
			contains: "unknown phase dependency",
		},
		{
			name:     "unknown repair actor",
			replace:  "repair: {once: coder}",
			with:     "repair: {once: missing}",
			path:     "spec.validation.tests.repair.once",
			contains: "unknown agent",
		},
		{
			name:     "dependency cycle",
			replace:  "- {id: implement, actor: coder, prompt: Implement the feature., validation: tests}",
			with:     "- {id: implement, actor: coder, prompt: Implement the feature., validation: tests, dependsOn: [review]}",
			path:     "spec.phases[0].dependsOn",
			contains: "dependency cycle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			document := strings.Replace(v1alpha2Fixture, tc.replace, tc.with, 1)
			result := ValidateFile(writeWorkflow(t, document))
			if result.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Path == tc.path && diagnostic.Position.Line > 0 && strings.Contains(diagnostic.Message, tc.contains) {
					return
				}
			}
			t.Fatalf("diagnostics = %#v", result.Diagnostics)
		})
	}
}

func TestV1Alpha2RepairPolicyFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		replace string
		with    string
		want    string
	}{
		{
			name:    "missing actor",
			replace: "repair: {once: coder}",
			with:    "repair: {}",
			want:    "repair.once is required",
		},
		{
			name:    "empty actor",
			replace: "repair: {once: coder}",
			with:    "repair: {once: ''}",
			want:    "repair.once is required",
		},
		{
			name:    "unknown actor",
			replace: "repair: {once: coder}",
			with:    "repair: {once: missing}",
			want:    "unknown agent",
		},
		{
			name:    "malformed scalar policy",
			replace: "repair: {once: coder}",
			with:    "repair: once",
			want:    "repair must be a mapping",
		},
		{
			name:    "malformed expanded policy",
			replace: "repair: {once: coder}",
			with:    "repair: {strategy: repair-once, maxRepairAttempts: 1, actor: coder}",
			want:    "field strategy not found",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, strings.Replace(v1alpha2Fixture, tc.replace, tc.with, 1)))
			if result.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if strings.Contains(diagnostic.Message, tc.want) {
					return
				}
			}
			t.Fatalf("diagnostics = %#v, want %q", result.Diagnostics, tc.want)
		})
	}
}

func TestV1Alpha2RepairAliasesStillRequireDeclaredActor(t *testing.T) {
	body := strings.Replace(v1alpha2Fixture, "model: gpt-5.6-terra", "model: &repair_actor missing", 1)
	body = strings.Replace(body, "repair: {once: coder}", "repair: {once: *repair_actor}", 1)
	result := ValidateFile(writeWorkflow(t, body))
	if result.Status != Invalid {
		t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Path == "spec.validation.tests.repair.once" && strings.Contains(diagnostic.Message, "unknown agent") {
			return
		}
	}
	t.Fatalf("diagnostics = %#v", result.Diagnostics)
}

func TestV1Alpha2RepairMergeCannotHideAuthority(t *testing.T) {
	_, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: merged-repair-policy}
spec:
  workspace: {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation:
    tests:
      run: make test
      repair:
        <<: {once: coder}
  phases: [{id: implement, actor: coder, prompt: implement, validation: tests}]
  completion: {validation: tests}
`))
	if err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported in spec.validation.tests.repair") {
		t.Fatalf("merge error = %v", err)
	}
}

func TestV1Alpha1RepairOnceCompatibilityRemainsBounded(t *testing.T) {
	d, err := Decode(writeWorkflow(t, conciseFixture))
	if err != nil {
		t.Fatal(err)
	}
	n, err := NormalizeWorkflow(d)
	if err != nil {
		t.Fatal(err)
	}
	v := n.Workflow.Spec.Validation["gate"]
	if v.OnFailure.Strategy != "repair-once" || v.OnFailure.MaxRepairAttempts != 1 || v.OnFailure.Repair.Actor != "worker" {
		t.Fatalf("v1alpha1 repair contract changed: %#v", v.OnFailure)
	}
}

func TestV1Alpha2PreservesScalarsAndRejectsMergeKeys(t *testing.T) {
	d, err := Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: scalar-values}
spec:
  workspace: {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation:
    tests:
      run: >-
        go test ./...
        && go vet ./...
  phases:
    - id: implement
      actor: coder
      prompt: |-
        Implement the feature.

        Keep the public API stable.
      validation: tests
  completion: {validation: tests}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := d.V1Alpha2.Spec.Validation["tests"].Run, "go test ./... && go vet ./..."; got != want {
		t.Fatalf("folded run = %q, want %q", got, want)
	}
	if got, want := d.Workflow.Spec.Phases[0].Prompt, "Implement the feature.\n\nKeep the public API stable."; got != want {
		t.Fatalf("literal prompt = %q, want %q", got, want)
	}

	_, err = Decode(writeWorkflow(t, `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: merged-authority}
spec:
  workspace:
    <<: &policy {allowWrites: [src/**]}
  agents: {coder: {runner: codex, model: gpt-5.6-terra}}
  validation: {tests: {run: "true"}}
  phases: [{id: implement, actor: coder, prompt: implement, validation: tests}]
  completion: {validation: tests}
`))
	if err == nil || !strings.Contains(err.Error(), "YAML merge keys are not supported in spec.workspace") {
		t.Fatalf("merge error = %v", err)
	}
}
