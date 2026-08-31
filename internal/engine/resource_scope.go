package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/internal/workspacepath"
)

func (e *Engine) effectivePhaseWrites(phase *workflow.Phase) ([]string, error) {
	if phase != nil && phase.ReadOnly {
		return []string{}, nil
	}
	patterns := e.Workflow.Spec.Workspace.MutationPolicy.Allowed
	if phase != nil && len(phase.Writes) != 0 {
		patterns = phase.Writes
	}
	context := e.context(phase)
	resolved := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		expanded, err := context.Expand(pattern)
		if err != nil {
			return nil, fmt.Errorf("expand phase write scope %q: %w", pattern, err)
		}
		cleaned, ok := workspacepath.Clean(expanded)
		if !ok {
			return nil, fmt.Errorf("phase write scope %q must be workspace-relative", expanded)
		}
		resolved = append(resolved, cleaned)
	}
	return resolved, nil
}

func (e *Engine) phaseResourceScope(phase *workflow.Phase) ([]string, error) {
	writes, err := e.effectivePhaseWrites(phase)
	if err != nil {
		return nil, err
	}
	owned, err := e.engineOwnedProgressFiles(e.context(phase), phase)
	if err != nil {
		return nil, err
	}
	for _, value := range owned {
		if filepath.IsAbs(value) {
			value, err = filepath.Rel(e.Repo.Root, value)
			if err != nil {
				return nil, err
			}
		}
		cleaned, ok := workspacepath.Clean(value)
		if !ok {
			return nil, fmt.Errorf("runtime-owned phase path %q must be workspace-relative", value)
		}
		writes = append(writes, cleaned)
	}
	sort.Strings(writes)
	return compactStrings(writes), nil
}

func phaseScopesConflict(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if pathPatternsMayOverlap(a, b) {
				return true
			}
		}
	}
	return false
}

// pathPatternsMayOverlap proves only obvious lexical disjointness. Ambiguous
// glob intersections serialize, which keeps conflict analysis fail closed.
func pathPatternsMayOverlap(left, right string) bool {
	if left == right || left == "**" || right == "**" {
		return true
	}
	a := literalPatternRoot(left)
	b := literalPatternRoot(right)
	if a == "" || b == "" {
		return true
	}
	return pathWithinOrEqual(a, b) || pathWithinOrEqual(b, a)
}

func literalPatternRoot(pattern string) string {
	index := strings.IndexAny(pattern, "*?")
	if index < 0 {
		return strings.TrimSuffix(strings.TrimSuffix(pattern, "/"), ".")
	}
	prefix := pattern[:index]
	separator := strings.LastIndex(prefix, "/")
	if separator < 0 {
		return ""
	}
	return strings.TrimSuffix(prefix[:separator], "/")
}

func pathWithinOrEqual(root, candidate string) bool {
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func runtimePatternCoveredByAny(allowed []string, candidate string) bool {
	for _, authored := range allowed {
		pattern, ok := workspacepath.Clean(authored)
		if !ok {
			continue
		}
		if pattern == candidate || pattern == "**" {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			root := strings.TrimSuffix(pattern, "/**")
			if candidate == root || strings.HasPrefix(candidate, root+"/") {
				return true
			}
		}
	}
	return false
}
