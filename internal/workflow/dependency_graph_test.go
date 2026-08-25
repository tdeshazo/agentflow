package workflow

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestV1Alpha2DependencyGraphPreservesAuthoredOrderAndEdges(t *testing.T) {
	tests := []struct {
		name        string
		phases      string
		wantNodes   []string
		wantEdges   [][2]string
		wantByPhase map[string][]string
	}{
		{
			name: "chain",
			phases: `
    - {id: design, actor: coder, prompt: design, validation: tests}
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [design]}
    - {id: review, actor: reviewer, prompt: review, validation: tests, dependsOn: [implement]}`,
			wantNodes: []string{"design", "implement", "review"},
			wantEdges: [][2]string{{"implement", "design"}, {"review", "implement"}},
			wantByPhase: map[string][]string{
				"design":    {},
				"implement": {"design"},
				"review":    {"implement"},
			},
		},
		{
			name: "fan out",
			phases: `
    - {id: prepare, actor: coder, prompt: prepare, validation: tests}
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [prepare]}
    - {id: document, actor: reviewer, prompt: document, validation: tests, dependsOn: [prepare]}`,
			wantNodes: []string{"prepare", "implement", "document"},
			wantEdges: [][2]string{{"implement", "prepare"}, {"document", "prepare"}},
			wantByPhase: map[string][]string{
				"prepare":   {},
				"implement": {"prepare"},
				"document":  {"prepare"},
			},
		},
		{
			name: "fan in",
			phases: `
    - {id: api, actor: coder, prompt: api, validation: tests}
    - {id: ui, actor: reviewer, prompt: ui, validation: tests}
    - {id: release, actor: coder, prompt: release, validation: tests, dependsOn: [api, ui]}`,
			wantNodes: []string{"api", "ui", "release"},
			wantEdges: [][2]string{{"release", "api"}, {"release", "ui"}},
			wantByPhase: map[string][]string{
				"api":     {},
				"ui":      {},
				"release": {"api", "ui"},
			},
		},
		{
			name: "multiple roots",
			phases: `
    - {id: research, actor: coder, prompt: research, validation: tests}
    - {id: scaffold, actor: reviewer, prompt: scaffold, validation: tests}
    - {id: integrate, actor: coder, prompt: integrate, validation: tests, dependsOn: [research, scaffold]}`,
			wantNodes: []string{"research", "scaffold", "integrate"},
			wantEdges: [][2]string{{"integrate", "research"}, {"integrate", "scaffold"}},
			wantByPhase: map[string][]string{
				"research":  {},
				"scaffold":  {},
				"integrate": {"research", "scaffold"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Decode(writeWorkflow(t, v1Alpha2Document(tt.phases)))
			if err != nil {
				t.Fatal(err)
			}
			result := Validate(d)
			if result.Status != Unsupported || result.Normalized == nil {
				t.Fatalf("status = %s, normalized = %#v, diagnostics = %#v", result.Status, result.Normalized, result.Diagnostics)
			}
			assertDependencyGraph(t, result.Normalized.DependencyGraph, tt.wantNodes, tt.wantEdges)

			plan, err := BuildExpandedPlan(d)
			if err != nil {
				t.Fatal(err)
			}
			assertDependencyGraph(t, plan.DependencyGraph, tt.wantNodes, tt.wantEdges)
			for _, phase := range plan.Phases {
				if got, want := strings.Join(phase.DependsOn, ","), strings.Join(tt.wantByPhase[phase.ID], ","); got != want {
					t.Fatalf("phase %q dependencies = %q, want %q", phase.ID, got, want)
				}
			}
		})
	}
}

func TestV1Alpha2DependencyGraphValidation(t *testing.T) {
	tests := []struct {
		name     string
		phases   string
		path     string
		contains string
	}{
		{
			name: "missing dependency",
			phases: `
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [missing]}`,
			path:     "spec.phases[0].dependsOn[0]",
			contains: `unknown phase dependency "missing"`,
		},
		{
			name: "self dependency",
			phases: `
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [implement]}`,
			path:     "spec.phases[0].dependsOn[0]",
			contains: "must not depend on itself",
		},
		{
			name: "duplicate phase ID",
			phases: `
    - {id: implement, actor: coder, prompt: implement, validation: tests}
    - {id: implement, actor: reviewer, prompt: review, validation: tests}`,
			path:     "spec.phases[1].id",
			contains: `duplicate phase id "implement"`,
		},
		{
			name: "duplicate dependency edge",
			phases: `
    - {id: implement, actor: coder, prompt: implement, validation: tests}
    - {id: review, actor: reviewer, prompt: review, validation: tests, dependsOn: [implement, implement]}`,
			path:     "spec.phases[1].dependsOn[1]",
			contains: `duplicate dependency "implement"`,
		},
		{
			name: "direct cycle",
			phases: `
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [review]}
    - {id: review, actor: reviewer, prompt: review, validation: tests, dependsOn: [implement]}`,
			path:     "spec.phases[0].dependsOn",
			contains: "dependency cycle: implement -> review -> implement",
		},
		{
			name: "multi node cycle",
			phases: `
    - {id: design, actor: coder, prompt: design, validation: tests, dependsOn: [review]}
    - {id: implement, actor: coder, prompt: implement, validation: tests, dependsOn: [design]}
    - {id: review, actor: reviewer, prompt: review, validation: tests, dependsOn: [implement]}`,
			path:     "spec.phases[0].dependsOn",
			contains: "dependency cycle: design -> review -> implement -> design",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidateFile(writeWorkflow(t, v1Alpha2Document(tt.phases)))
			if result.Status != Invalid {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Path == tt.path && strings.Contains(diagnostic.Message, tt.contains) {
					return
				}
			}
			t.Fatalf("diagnostics = %#v", result.Diagnostics)
		})
	}
}

func TestV1Alpha2DependencyGraphRepresentationIsDeterministic(t *testing.T) {
	d, err := Decode(writeWorkflow(t, v1Alpha2Document(`
    - {id: first, actor: coder, prompt: first, validation: tests}
    - {id: second, actor: reviewer, prompt: second, validation: tests, dependsOn: [first]}
    - {id: third, actor: coder, prompt: third, validation: tests, dependsOn: [first, second]}`)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := yaml.Marshal(d.DependencyGraph)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		normalized, err := NormalizeWorkflow(d)
		if err != nil {
			t.Fatal(err)
		}
		got, err := yaml.Marshal(normalized.DependencyGraph)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("iteration %d graph changed:\n%s\nwant:\n%s", i, got, want)
		}
	}
}

func assertDependencyGraph(t *testing.T, graph PhaseDependencyGraph, wantNodes []string, wantEdges [][2]string) {
	t.Helper()
	if len(graph.Nodes) != len(wantNodes) {
		t.Fatalf("graph nodes = %#v, want %v", graph.Nodes, wantNodes)
	}
	for i, want := range wantNodes {
		if got := graph.Nodes[i].ID; got != want || graph.Nodes[i].AuthoredOrder != i {
			t.Fatalf("node %d = %#v, want ID %q and authored order %d", i, graph.Nodes[i], want, i)
		}
	}
	if len(graph.Edges) != len(wantEdges) {
		t.Fatalf("graph edges = %#v, want %v", graph.Edges, wantEdges)
	}
	for i, want := range wantEdges {
		edge := graph.Edges[i]
		if edge.Phase != want[0] || edge.DependsOn != want[1] || edge.SatisfiedWhen != PhaseDependencyAccepted {
			t.Fatalf("edge %d = %#v, want phase %q depends on %q after deterministic acceptance", i, edge, want[0], want[1])
		}
	}
}

func v1Alpha2Document(phases string) string {
	return `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: dependency-graph}
spec:
  workspace: {allowWrites: [src/**]}
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
    reviewer: {runner: codex, model: gpt-5.6-luna}
  validation: {tests: {run: "true"}}
  phases:` + phases + `
  completion: {validation: tests}
`
}
