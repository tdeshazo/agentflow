package workflow

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var exprRE = regexp.MustCompile(`\{\{\s*(.*?)\s*\}\}`)
var envDefaultRE = regexp.MustCompile(`^env\.([A-Za-z_][A-Za-z0-9_]*)\s*\|\s*default\(['\"](.*)['\"]\)$`)

// Context resolves the deliberately small expression subset used by the
// executable v1alpha1 examples. Unknown expressions fail closed.
type Context struct {
	Metadata     Metadata
	Parameters   map[string]any
	Paths        map[string]string
	State        map[string]any
	Phase        *Phase
	WorkflowFile string
	FailureLog   string
	HeadCommit   string
}

func (c Context) Expand(s string) (string, error) {
	var firstErr error
	out := exprRE.ReplaceAllStringFunc(s, func(m string) string {
		if firstErr != nil {
			return m
		}
		expr := strings.TrimSpace(exprRE.FindStringSubmatch(m)[1])
		v, err := c.eval(expr)
		if err != nil {
			firstErr = err
			return m
		}
		return fmt.Sprint(v)
	})
	return out, firstErr
}

func (c Context) Bool(expr string) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	if strings.HasPrefix(expr, "{{") && strings.HasSuffix(expr, "}}") {
		expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(expr, "{{"), "}}"))
	}
	if strings.HasPrefix(expr, "not ") {
		v, err := c.Bool("{{ " + strings.TrimSpace(strings.TrimPrefix(expr, "not ")) + " }}")
		return !v, err
	}
	v, err := c.eval(expr)
	if err != nil {
		return false, err
	}
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		return strconv.ParseBool(x)
	default:
		return false, fmt.Errorf("expression %q is not boolean", expr)
	}
}

func (c Context) eval(expr string) (any, error) {
	if m := envDefaultRE.FindStringSubmatch(expr); m != nil {
		if v := os.Getenv(m[1]); v != "" {
			return v, nil
		}
		return m[2], nil
	}
	if strings.HasPrefix(expr, "tail(validation.failure.log,") {
		return tailLines(c.FailureLog, parseTailCount(expr)), nil
	}
	switch expr {
	case "metadata.name":
		return c.Metadata.Name, nil
	case "workflow.file", "workflow.file | default('')":
		return c.WorkflowFile, nil
	case "head_commit":
		return c.HeadCommit, nil
	}
	if strings.HasPrefix(expr, "parameters.") {
		k := strings.TrimPrefix(expr, "parameters.")
		v, ok := c.Parameters[k]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", k)
		}
		return v, nil
	}
	if strings.HasPrefix(expr, "spec.paths.") {
		k := strings.TrimPrefix(expr, "spec.paths.")
		v, ok := c.Paths[k]
		if !ok {
			return nil, fmt.Errorf("unknown spec path %q", k)
		}
		return v, nil
	}
	if strings.HasPrefix(expr, "state.") {
		parts := strings.Split(strings.TrimPrefix(expr, "state."), ".")
		v, ok := c.State[parts[0]]
		if !ok {
			return nil, fmt.Errorf("unknown state value %q", parts[0])
		}
		if len(parts) == 1 {
			return v, nil
		}
		if m, ok := v.(map[string]any); ok {
			if x, ok := m[parts[1]]; ok {
				return x, nil
			}
		}
		return nil, fmt.Errorf("unknown state expression %q", expr)
	}
	if strings.HasPrefix(expr, "phase.") && c.Phase != nil {
		switch strings.TrimPrefix(expr, "phase.") {
		case "id":
			return c.Phase.ID, nil
		case "label":
			return c.Phase.Label, nil
		case "kind":
			return c.Phase.Kind, nil
		case "criterion":
			return c.Phase.Criterion, nil
		case "requiresChange":
			return c.Phase.RequiresChange, nil
		}
	}
	if expr == "true" {
		return true, nil
	}
	if expr == "false" {
		return false, nil
	}
	return nil, fmt.Errorf("unsupported template expression %q", expr)
}

func parseTailCount(expr string) int {
	start := strings.LastIndex(expr, ",")
	end := strings.LastIndex(expr, ")")
	if start < 0 || end < start {
		return 200
	}
	n, err := strconv.Atoi(strings.TrimSpace(expr[start+1 : end]))
	if err != nil || n <= 0 {
		return 200
	}
	return n
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
