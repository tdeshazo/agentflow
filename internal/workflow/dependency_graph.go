package workflow

// PhaseDependencyAccepted is the only state that satisfies a phase
// dependency. Running an actor, passing a partial check, or retaining an
// interrupted active phase is not enough: the prerequisite must complete the
// runtime's deterministic acceptance boundary.
const PhaseDependencyAccepted = "deterministically accepted"

// PhaseDependencyGraph is the normalized, ordered declaration of phase
// dependencies. Nodes preserve authoring order so a future scheduler has an
// explicit, deterministic tie breaker without relying on Go map iteration.
//
// This is descriptive only. It does not select or schedule phases.
type PhaseDependencyGraph struct {
	Nodes []PhaseDependencyNode `yaml:"nodes"`
	Edges []PhaseDependencyEdge `yaml:"edges"`
}

// PhaseDependencyNode identifies one authored phase and its stable position
// in the document.
type PhaseDependencyNode struct {
	ID            string `yaml:"id"`
	AuthoredOrder int    `yaml:"authoredOrder"`
}

// PhaseDependencyEdge declares that Phase cannot proceed until DependsOn has
// reached SatisfiedWhen. Edges are kept in authored phase and dependsOn order.
type PhaseDependencyEdge struct {
	Phase         string `yaml:"phase"`
	DependsOn     string `yaml:"dependsOn"`
	SatisfiedWhen string `yaml:"satisfiedWhen"`

	phaseIndex      int
	dependencyIndex int
}

// buildV1Alpha2PhaseDependencyGraph constructs a stable graph directly from
// the authored phase sequence. It deliberately performs no validation: the
// validator uses this lossless graph to report all source errors with precise
// paths.
func buildV1Alpha2PhaseDependencyGraph(phases []V1Alpha2Phase) PhaseDependencyGraph {
	graph := PhaseDependencyGraph{
		Nodes: make([]PhaseDependencyNode, 0, len(phases)),
		Edges: make([]PhaseDependencyEdge, 0),
	}
	for phaseIndex, phase := range phases {
		graph.Nodes = append(graph.Nodes, PhaseDependencyNode{
			ID:            phase.ID,
			AuthoredOrder: phaseIndex,
		})
		for dependencyIndex, dependency := range phase.DependsOn {
			graph.Edges = append(graph.Edges, PhaseDependencyEdge{
				Phase:           phase.ID,
				DependsOn:       dependency,
				SatisfiedWhen:   PhaseDependencyAccepted,
				phaseIndex:      phaseIndex,
				dependencyIndex: dependencyIndex,
			})
		}
	}
	return graph
}

// phaseIndex returns the first authored occurrence of each ID. Validation
// separately rejects duplicate IDs; choosing the first here keeps every
// diagnostic deterministic while the document is invalid.
func (g PhaseDependencyGraph) phaseIndex() map[string]int {
	index := make(map[string]int, len(g.Nodes))
	for i, node := range g.Nodes {
		if _, exists := index[node.ID]; !exists {
			index[node.ID] = i
		}
	}
	return index
}

func (g PhaseDependencyGraph) edgesForPhase(phaseIndex int) []PhaseDependencyEdge {
	edges := make([]PhaseDependencyEdge, 0)
	for _, edge := range g.Edges {
		if edge.phaseIndex == phaseIndex {
			edges = append(edges, edge)
		}
	}
	return edges
}

func (g PhaseDependencyGraph) dependenciesForPhase(phaseIndex int) []string {
	edges := g.edgesForPhase(phaseIndex)
	dependencies := make([]string, 0, len(edges))
	for _, edge := range edges {
		dependencies = append(dependencies, edge.DependsOn)
	}
	return dependencies
}

// phaseDependenciesMap preserves the former Document field for compatibility.
// New code must use PhaseDependencyGraph, whose slice-based representation is
// stable and also records the required acceptance state of every edge.
func (g PhaseDependencyGraph) phaseDependenciesMap() map[string][]string {
	dependencies := make(map[string][]string, len(g.Nodes))
	for phaseIndex, node := range g.Nodes {
		edges := g.dependenciesForPhase(phaseIndex)
		if len(edges) > 0 {
			dependencies[node.ID] = edges
		}
	}
	return dependencies
}

func clonePhaseDependencyGraph(graph PhaseDependencyGraph) PhaseDependencyGraph {
	return PhaseDependencyGraph{
		Nodes: append([]PhaseDependencyNode(nil), graph.Nodes...),
		Edges: append([]PhaseDependencyEdge(nil), graph.Edges...),
	}
}
