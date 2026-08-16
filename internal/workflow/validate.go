package workflow

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Validate performs document-only checks. It intentionally does not expand
// templates, resolve a repository, create Git state, or construct an engine.
func Validate(d *Document) Result {
	r := Result{Status: Executable, Document: d}
	if d == nil || d.Workflow == nil {
		return Result{Status: Invalid, Document: d, Diagnostics: []Diagnostic{{Status: Invalid, Message: "empty workflow document"}}}
	}
	v := validator{result: &r, locations: d.Locations, w: d.Workflow}
	v.roots()
	v.references()
	v.expressions()
	v.runtimeSurface()
	for _, x := range r.Diagnostics {
		if x.Status == Invalid {
			r.Status = Invalid
			return r
		}
	}
	for _, x := range r.Diagnostics {
		if x.Status == Unsupported {
			r.Status = Unsupported
			break
		}
	}
	return r
}

type validator struct {
	result    *Result
	locations Locations
	w         *Workflow
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (v validator) add(status Status, path, format string, args ...any) {
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{Status: status, Path: path, Position: v.location(path), Message: fmt.Sprintf(format, args...)})
}
func (v validator) location(path string) Position {
	for path != "" {
		if p, ok := v.locations[path]; ok {
			return p
		}
		i := strings.LastIndexAny(path, ".[")
		if i < 0 {
			break
		}
		path = path[:i]
	}
	return Position{}
}
func (v validator) roots() {
	if v.w.APIVersion != "agentflow.dev/v1alpha1" {
		v.add(Invalid, "apiVersion", "must be agentflow.dev/v1alpha1 (got %q)", v.w.APIVersion)
	}
	if v.w.Kind != "AgentWorkflow" {
		v.add(Invalid, "kind", "must be AgentWorkflow (got %q)", v.w.Kind)
	}
	if v.w.Metadata.Name == "" {
		v.add(Invalid, "metadata.name", "is required")
	}
	for _, n := range sortedKeys(v.w.Spec.Parameters) {
		p := v.w.Spec.Parameters[n]
		if n == "" {
			v.add(Invalid, "spec.parameters", "parameter name must not be empty")
		}
		switch p.Type {
		case "string", "path", "boolean", "integer":
		default:
			v.add(Invalid, "spec.parameters."+n+".type", "unknown parameter type %q", p.Type)
		}
		v.parameterDefault(n, p)
	}
	v.uniqueChecks()
	v.uniquePhases()
	v.uniqueCriteria()
	v.uniqueGates()
	v.uniqueIntegrity()
}
func (v validator) parameterDefault(name string, p Parameter) {
	if p.Default == nil {
		return
	}
	k := reflect.TypeOf(p.Default).Kind()
	path := "spec.parameters." + name + ".default"
	switch p.Type {
	case "boolean":
		if k != reflect.Bool {
			v.add(Invalid, path, "boolean parameter default must be a YAML boolean")
		}
	case "integer":
		if k < reflect.Int || k > reflect.Int64 {
			v.add(Invalid, path, "integer parameter default must be a YAML integer")
		}
	case "string", "path":
		if k != reflect.String {
			v.add(Invalid, path, "%s parameter default must be a YAML string", p.Type)
		}
	}
}
func (v validator) uniqueChecks() {
	seen := map[string]bool{}
	for i, c := range v.w.Spec.Preconditions {
		p := fmt.Sprintf("spec.preconditions[%d].id", i)
		if c.ID == "" {
			v.add(Invalid, p, "is required")
		} else if seen[c.ID] {
			v.add(Invalid, p, "duplicate check id %q", c.ID)
		}
		seen[c.ID] = true
	}
}
func (v validator) uniquePhases() {
	seen := map[string]bool{}
	for i, p := range v.w.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d].id", i)
		if p.ID == "" {
			v.add(Invalid, path, "is required")
		} else if seen[p.ID] {
			v.add(Invalid, path, "duplicate phase id %q", p.ID)
		}
		seen[p.ID] = true
		if p.Kind == "" {
			v.add(Invalid, fmt.Sprintf("spec.phases[%d].kind", i), "is required")
		}
	}
}
func (v validator) uniqueCriteria() {
	seen := map[string]bool{}
	for i, c := range v.w.Spec.Progress.Criteria {
		path := fmt.Sprintf("spec.progress.criteria[%d].id", i)
		if c.ID == "" {
			v.add(Invalid, path, "is required")
		} else if seen[c.ID] {
			v.add(Invalid, path, "duplicate criterion id %q", c.ID)
		}
		seen[c.ID] = true
	}
}
func (v validator) uniqueGates() {
	seen := map[string]bool{}
	for i, g := range v.w.Spec.HumanGates {
		p := fmt.Sprintf("spec.humanGates[%d].id", i)
		if g.ID == "" {
			v.add(Invalid, p, "is required")
		} else if seen[g.ID] {
			v.add(Invalid, p, "duplicate human gate id %q", g.ID)
		}
		seen[g.ID] = true
	}
}
func (v validator) uniqueIntegrity() {
	seen := map[string]bool{}
	for i, r := range v.w.Spec.Workspace.MutationPolicy.Integrity {
		p := fmt.Sprintf("spec.workspace.mutationPolicy.integrity[%d].id", i)
		if r.ID == "" {
			v.add(Invalid, p, "is required")
		} else if seen[r.ID] {
			v.add(Invalid, p, "duplicate integrity rule id %q", r.ID)
		}
		seen[r.ID] = true
	}
}

func (v validator) references() {
	for _, name := range sortedKeys(v.w.Spec.Validation) {
		validation := v.w.Spec.Validation[name]
		v.toolUses("spec.validation."+name+".steps", validation.Steps)
		v.toolUses("spec.validation."+name+".onFailure.then", validation.OnFailure.Then)
		if a := validation.OnFailure.Repair.Actor; a != "" {
			v.agent("spec.validation."+name+".onFailure.repair.actor", a)
		}
	}
	for i, p := range v.w.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", i)
		if p.Actor == "" {
			v.add(Invalid, path+".actor", "is required")
		} else {
			v.agent(path+".actor", p.Actor)
		}
		if p.Kind == "criterion" {
			if p.Criterion == "" {
				v.add(Invalid, path+".criterion", "criterion phases require a criterion")
			} else if len(v.w.Spec.Progress.Criteria) > 0 && !v.criterion(p.Criterion) {
				v.add(Invalid, path+".criterion", "unknown criterion %q", p.Criterion)
			}
		}
		v.actions(path+".after", p.After)
	}
	v.actions("spec.phaseDefaults.before", v.w.Spec.PhaseDefaults.Before)
	v.actions("spec.phaseDefaults.after", v.w.Spec.PhaseDefaults.After)
	for i, g := range v.w.Spec.HumanGates {
		for j, a := range g.After {
			p := fmt.Sprintf("spec.humanGates[%d].after[%d]", i, j)
			if a.Phase != "" {
				v.phase(p+".phase", a.Phase)
			}
			if a.Validation != "" {
				v.validationOrFlow(p+".validation", a.Validation)
			}
		}
	}
	for i, s := range v.w.Spec.Flow {
		p := fmt.Sprintf("spec.flow[%d]", i)
		n := 0
		if s.Phase != "" {
			n++
			v.phase(p+".phase", s.Phase)
		}
		if s.Human != "" {
			n++
			v.gate(p+".human", s.Human)
		}
		if s.Validate != "" {
			n++
			v.validation(p+".validate", s.Validate)
		}
		if s.Checkpoint != "" {
			n++
			v.tool(p+".checkpoint", s.Checkpoint)
		}
		if s.Complete != "" {
			n++
			v.completion(p+".complete", s.Complete)
		}
		if s.Recover != "" {
			n++
		}
		if s.Assert != nil {
			n++
		}
		if s.If == "" && n == 0 {
			v.add(Invalid, p, "must contain an executable action")
		}
	}
	for _, name := range sortedKeys(v.w.Spec.Completion) {
		c := v.w.Spec.Completion[name]
		p := "spec.completion." + name
		if c.FinalValidation != "" {
			v.validation(p+".finalValidation", c.FinalValidation)
		}
		if c.Checkpoint.Uses != "" {
			v.tool(p+".checkpoint.uses", c.Checkpoint.Uses)
		}
		v.assertions(p+".assertions", c.Assertions)
		v.assertions(p+".afterCheckpointAssertions", c.AfterCheckpointAssertions)
	}
}
func (v validator) toolUses(path string, uses []ToolUse) {
	for i, u := range uses {
		if u.Uses == "" {
			v.add(Invalid, fmt.Sprintf("%s[%d].uses", path, i), "is required")
		} else {
			v.tool(fmt.Sprintf("%s[%d].uses", path, i), u.Uses)
		}
	}
}
func (v validator) actions(path string, actions []PhaseAction) {
	for i, a := range actions {
		p := fmt.Sprintf("%s[%d]", path, i)
		if a.Validate != "" {
			v.validation(p+".validate", a.Validate)
		}
		if a.Checkpoint != "" {
			v.tool(p+".checkpoint", a.Checkpoint)
		}
	}
}
func (v validator) assertions(path string, as []Assertion) {
	for i, a := range as {
		if a.Uses != "" {
			v.tool(fmt.Sprintf("%s[%d].uses", path, i), a.Uses)
		}
	}
}
func (v validator) agent(path, id string) {
	if _, ok := v.w.Spec.Agents[id]; !ok {
		v.add(Invalid, path, "unknown agent %q", id)
	}
}
func (v validator) tool(path, id string) {
	if _, ok := v.w.Spec.Tools[id]; !ok {
		v.add(Invalid, path, "unknown tool %q", id)
	}
}
func (v validator) phase(path, id string) {
	for _, p := range v.w.Spec.Phases {
		if p.ID == id {
			return
		}
	}
	v.add(Invalid, path, "unknown phase %q", id)
}
func (v validator) gate(path, id string) {
	for _, g := range v.w.Spec.HumanGates {
		if g.ID == id {
			return
		}
	}
	v.add(Invalid, path, "unknown human gate %q", id)
}
func (v validator) validation(path, id string) {
	if _, ok := v.w.Spec.Validation[id]; !ok {
		v.add(Invalid, path, "unknown validation %q", id)
	}
}
func (v validator) validationOrFlow(path, id string) {
	if _, ok := v.w.Spec.Validation[id]; ok {
		return
	}
	for _, s := range v.w.Spec.Flow {
		if s.ID == id && s.Validate != "" {
			return
		}
	}
	v.add(Invalid, path, "unknown validation or validation flow step %q", id)
}
func (v validator) completion(path, id string) {
	if _, ok := v.w.Spec.Completion[id]; !ok {
		v.add(Invalid, path, "unknown completion %q", id)
	}
}
func (v validator) criterion(id string) bool {
	for _, c := range v.w.Spec.Progress.Criteria {
		if c.ID == id || c.Text == id {
			return true
		}
	}
	return false
}

// expressions checks the small amount of expression structure that can be
// validated without repository or runtime state. It deliberately does not
// evaluate conditionals or add runtime expression features; it only catches
// malformed delimiters and references to statically-known names.
func (v validator) expressions() {
	visitStrings(reflect.ValueOf(v.w), "", func(path, value string) {
		if !strings.Contains(value, "{{") && !strings.Contains(value, "}}") {
			return
		}
		validateTemplateString(path, value, v.w.Spec.Parameters, v.w.Spec.Paths, func(message string) {
			v.add(Invalid, path, "invalid expression: %s", message)
		})
	})
}

func visitStrings(value reflect.Value, path string, visit func(string, string)) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		visitStrings(value.Elem(), path, visit)
		return
	}
	switch value.Kind() {
	case reflect.String:
		visit(path, value.String())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if tag == "" || tag == "-" || !field.IsExported() {
				continue
			}
			child := tag
			if path != "" {
				child = path + "." + tag
			}
			visitStrings(value.Field(i), child, visit)
		}
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return
		}
		keys := make([]string, 0, value.Len())
		for _, key := range value.MapKeys() {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := key
			if path != "" {
				child = path + "." + key
			}
			visitStrings(value.MapIndex(reflect.ValueOf(key)), child, visit)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			visitStrings(value.Index(i), fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

var expressionReferenceRE = regexp.MustCompile(`\b(parameters|spec\.paths|metadata|phase)\.([A-Za-z_][A-Za-z0-9_-]*)`)
var expressionTokenRE = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_-]*(?:\.[A-Za-z_][A-Za-z0-9_-]*)*\b`)
var expressionLiteralRE = regexp.MustCompile(`'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"`)
var malformedExpressionReferenceRE = regexp.MustCompile(`\b(parameters|metadata|phase|spec(?:\.paths)?)\s*\.\s*(?:$|[^A-Za-z0-9_-])`)

func validateTemplateString(path, value string, parameters map[string]Parameter, paths map[string]string, invalid func(string)) {
	for offset := 0; offset < len(value); {
		open := strings.Index(value[offset:], "{{")
		close := strings.Index(value[offset:], "}}")
		if close >= 0 && (open < 0 || close < open) {
			invalid("unmatched closing delimiter")
			return
		}
		if open < 0 {
			return
		}
		open += offset
		end := strings.Index(value[open+2:], "}}")
		if end < 0 {
			invalid("missing closing delimiter")
			return
		}
		end += open + 2
		expr := strings.TrimSpace(value[open+2 : end])
		if message := validateExpressionReference(expr, parameters, paths); message != "" {
			invalid(message)
		}
		offset = end + 2
	}
}

func validateExpressionReference(expr string, parameters map[string]Parameter, paths map[string]string) string {
	if expr == "" {
		return "expression must not be empty"
	}
	parts := strings.Split(expr, "|")
	if len(parts) > 2 {
		return "expression has too many filters"
	}
	base := strings.TrimSpace(parts[0])
	if base == "" {
		return "expression base must not be empty"
	}
	if len(parts) == 2 {
		filter := strings.TrimSpace(parts[1])
		if !strings.HasPrefix(filter, "default(") || !strings.HasSuffix(filter, ")") {
			return "only the default filter is supported"
		}
		argument := strings.TrimSpace(filter[len("default(") : len(filter)-1])
		if len(argument) < 2 || argument[0] != argument[len(argument)-1] || (argument[0] != '\'' && argument[0] != '"') {
			return "default filter requires a quoted argument"
		}
	}

	// String literals are not references. This also permits shell-like text in
	// mktemp arguments while still checking the expression around it.
	withoutLiterals := expressionLiteralRE.ReplaceAllString(base, " ")
	allowedRoots := map[string]bool{
		"env": true, "head_commit": true, "invocation": true, "metadata": true,
		"mktemp": true, "parameters": true, "phase": true, "progress": true,
		"spec": true, "state": true, "tail": true, "temp": true,
		"validation": true, "workflow": true, "true": true, "false": true,
	}
	for _, token := range expressionTokenRE.FindAllString(withoutLiterals, -1) {
		root := strings.Split(token, ".")[0]
		if root == "not" || root == "and" || root == "or" {
			continue
		}
		if !allowedRoots[root] {
			return fmt.Sprintf("unknown expression reference %q", root)
		}
		if token == "parameters" || token == "metadata" || token == "phase" || token == "spec" || token == "spec.paths" {
			return fmt.Sprintf("incomplete expression reference %q", token)
		}
	}
	if malformedExpressionReferenceRE.MatchString(withoutLiterals) {
		return "incomplete expression reference"
	}

	for _, match := range expressionReferenceRE.FindAllStringSubmatch(base, -1) {
		switch match[1] {
		case "parameters":
			if _, ok := parameters[match[2]]; !ok {
				return fmt.Sprintf("unknown parameter reference %q", match[2])
			}
		case "spec.paths":
			if _, ok := paths[match[2]]; !ok {
				return fmt.Sprintf("unknown spec path reference %q", match[2])
			}
		case "metadata":
			if match[2] != "name" {
				return fmt.Sprintf("unknown metadata reference %q", match[2])
			}
		case "phase":
			switch match[2] {
			case "id", "label", "kind", "criterion", "requiresChange":
			default:
				return fmt.Sprintf("unknown phase reference %q", match[2])
			}
		}
	}
	return ""
}

func (v validator) runtimeSurface() {
	for _, name := range sortedKeys(v.w.Spec.Agents) {
		a := v.w.Spec.Agents[name]
		if a.Runner != "codex" {
			v.add(Unsupported, "spec.agents."+name+".runner", "runner %q is not implemented by this runtime", a.Runner)
		}
		if a.Approval != "" && a.Approval != "never" {
			v.add(Unsupported, "spec.agents."+name+".approval", "approval policy %q is not implemented", a.Approval)
		}
	}
	for _, name := range sortedKeys(v.w.Spec.Tools) {
		t := v.w.Spec.Tools[name]
		switch t.Type {
		case "shell", "workspace-policy", "git-checkpoint":
		default:
			v.add(Unsupported, "spec.tools."+name+".type", "tool type %q is not implemented by this runtime", t.Type)
		}
		if !reflect.DeepEqual(t.Capture, Capture{}) {
			v.add(Unsupported, "spec.tools."+name+".capture", "tool output capture is not implemented by this runtime")
		}
	}
	if !reflect.DeepEqual(v.w.Spec.PhaseDefaults, PhaseDefaults{}) {
		v.add(Unsupported, "spec.phaseDefaults", "phase lifecycle declarations are not implemented by this runtime")
	}
	if !reflect.DeepEqual(v.w.Spec.Recovery, Recovery{}) {
		v.add(Unsupported, "spec.recovery", "declarative recovery is not implemented by this runtime")
	}
	for i, p := range v.w.Spec.Phases {
		if len(p.After) > 0 {
			v.add(Unsupported, fmt.Sprintf("spec.phases[%d].after", i), "per-phase lifecycle declarations are not implemented by this runtime")
		}
	}
	for i, g := range v.w.Spec.HumanGates {
		if len(g.After) > 0 || g.Evidence.Record != "" || g.IdempotentRecord != "" {
			v.add(Unsupported, fmt.Sprintf("spec.humanGates[%d]", i), "human-gate placement/evidence declarations are not implemented by this runtime")
		}
	}
	for i, s := range v.w.Spec.Flow {
		n := 0
		for _, set := range []bool{s.Phase != "", s.Human != "", s.Validate != "", s.Checkpoint != "", s.Complete != "", s.Recover != "", s.Assert != nil} {
			if set {
				n++
			}
		}
		if n > 1 {
			v.add(Unsupported, fmt.Sprintf("spec.flow[%d]", i), "multi-action flow steps are not implemented by this runtime")
		}
	}
	for _, name := range sortedKeys(v.w.Spec.Completion) {
		c := v.w.Spec.Completion[name]
		if c.WriteMarker.Record != "" || !reflect.DeepEqual(c.Summary, Summary{}) {
			v.add(Unsupported, "spec.completion."+name, "custom completion marker/summary declarations are not implemented by this runtime")
		}
	}
}
