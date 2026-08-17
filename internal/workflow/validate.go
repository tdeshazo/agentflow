package workflow

import (
	"fmt"
	"reflect"
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
		if p.Env != "" && !validEnvironmentName(p.Env) {
			v.add(Invalid, "spec.parameters."+n+".env", "invalid environment variable name %q", p.Env)
		}
	}
	v.uniqueChecks()
	v.uniquePhases()
	v.uniqueCriteria()
	v.uniqueGates()
	v.uniqueIntegrity()
	if strategy := v.w.Spec.Progress.Selection.Strategy; strategy != "" && strategy != "first-unchecked" {
		v.add(Invalid, "spec.progress.selection.strategy", "unsupported progress selection strategy %q", strategy)
	}
}
func (v validator) parameterDefault(name string, p Parameter) {
	if p.Default == nil {
		return
	}
	k := reflect.TypeOf(p.Default).Kind()
	path := "spec.parameters." + name + ".default"
	if value, ok := p.Default.(string); ok && strings.Contains(value, "{{") {
		return // The expression is validated separately and its evaluated type is checked at runtime.
	}
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
		} else {
			switch p.Kind {
			case "criterion", "implementation", "audit", "bookkeeping":
			case "tool", "human":
				v.add(Unsupported, fmt.Sprintf("spec.phases[%d].kind", i), "phase kind %q is documented but not executable in this runtime", p.Kind)
			default:
				v.add(Invalid, fmt.Sprintf("spec.phases[%d].kind", i), "unsupported executable phase kind %q", p.Kind)
			}
		}
	}
}
func (v validator) uniqueCriteria() {
	seen := map[string]bool{}
	texts := map[string]bool{}
	for i, c := range v.w.Spec.Progress.Criteria {
		path := fmt.Sprintf("spec.progress.criteria[%d].id", i)
		if c.ID == "" {
			v.add(Invalid, path, "is required")
		} else if seen[c.ID] {
			v.add(Invalid, path, "duplicate criterion id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Text == "" {
			v.add(Invalid, fmt.Sprintf("spec.progress.criteria[%d].text", i), "is required")
		} else if texts[c.Text] {
			v.add(Invalid, fmt.Sprintf("spec.progress.criteria[%d].text", i), "duplicate criterion text %q is ambiguous", c.Text)
		}
		texts[c.Text] = true
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
		if len(r.Paths) == 0 {
			v.add(Invalid, strings.TrimSuffix(p, ".id")+".paths", "must protect at least one path")
		}
		switch r.Mode {
		case "exact-hash", "group-exact-hash":
			if r.Normalize.Command != "" {
				v.add(Invalid, strings.TrimSuffix(p, ".id")+".normalize.command", "is only valid with normalized-hash integrity")
			}
		case "normalized-hash":
			if r.Normalize.Command == "" {
				v.add(Invalid, strings.TrimSuffix(p, ".id")+".normalize.command", "is required for normalized-hash integrity")
			}
		default:
			v.add(Invalid, strings.TrimSuffix(p, ".id")+".mode", "unknown integrity mode %q", r.Mode)
		}
	}
}

func (v validator) references() {
	for _, name := range sortedKeys(v.w.Spec.Validation) {
		validation := v.w.Spec.Validation[name]
		path := "spec.validation." + name
		if len(validation.Steps) == 0 {
			v.add(Invalid, path+".steps", "must contain at least one deterministic validation step")
		}
		v.toolUses(path+".steps", validation.Steps)
		v.toolUses(path+".onFailure.then", validation.OnFailure.Then)
		v.validationFailurePolicy(path, validation)
	}
	v.lifecycle()
	for i, check := range v.w.Spec.Preconditions {
		v.check(fmt.Sprintf("spec.preconditions[%d]", i), check)
		v.condition(fmt.Sprintf("spec.preconditions[%d].when", i), check.When)
	}
	for i, p := range v.w.Spec.Phases {
		path := fmt.Sprintf("spec.phases[%d]", i)
		engineBookkeeping := p.Kind == "bookkeeping" && len(p.Bookkeeping) > 0
		if engineBookkeeping && p.Actor != "" {
			v.add(Invalid, path+".actor", "engine-owned bookkeeping phases must not declare an actor")
		} else if !engineBookkeeping && p.Actor == "" {
			v.add(Invalid, path+".actor", "is required")
		} else if p.Actor != "" {
			v.agent(path+".actor", p.Actor)
		}
		if p.Kind == "criterion" {
			if p.Criterion != "" && p.CriterionID != "" {
				v.add(Invalid, path, "must declare only one of criterion or criterionID")
			} else if p.CriterionID != "" {
				if !v.criterionID(p.CriterionID) {
					v.add(Invalid, path+".criterionID", "unknown criterion id %q", p.CriterionID)
				}
			} else if p.Criterion == "" {
				v.add(Invalid, path+".criterion", "criterion phases require a criterionID (or legacy criterion selector)")
			} else if len(v.w.Spec.Progress.Criteria) > 0 && !v.criterion(p.Criterion) {
				v.add(Invalid, path+".criterion", "unknown criterion %q", p.Criterion)
			}
		} else if p.CriterionID != "" || p.AdvanceProgress {
			v.add(Invalid, path, "criterionID and advanceProgress are only valid for criterion phases")
		}
		if len(p.Bookkeeping) > 0 && p.Kind != "bookkeeping" {
			v.add(Invalid, path+".bookkeeping", "is only valid for bookkeeping phases")
		}
		for j, transition := range p.Bookkeeping {
			v.markdownTransition(fmt.Sprintf("%s.bookkeeping[%d]", path, j), transition)
		}
		if p.Validation != "" {
			v.validation(path+".validation", p.Validation)
		}
		v.actions(path+".after", p.After)
		v.condition(path+".if", p.If)
	}
	v.actions("spec.phaseDefaults.before", v.w.Spec.PhaseDefaults.Before)
	v.actions("spec.phaseDefaults.after", v.w.Spec.PhaseDefaults.After)
	for i, g := range v.w.Spec.HumanGates {
		v.condition(fmt.Sprintf("spec.humanGates[%d].when", i), g.When)
		v.condition(fmt.Sprintf("spec.humanGates[%d].if", i), g.If)
		if g.When != "" && g.If != "" {
			v.add(Invalid, fmt.Sprintf("spec.humanGates[%d]", i), "must not declare both when and if")
		}
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
			if s.Recover != "activePhase" {
				v.add(Invalid, p+".recover", "unsupported recovery target %q", s.Recover)
			}
		}
		if s.Assert != nil {
			n++
			v.assertion(p+".assert", *s.Assert)
		}
		if s.Loop != nil {
			n++
			v.loop(p+".loop", *s.Loop)
		}
		v.condition(p+".if", s.If)
		if n == 0 && len(s.Then) == 0 {
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

func (v validator) lifecycle() {
	lifecycle := v.w.Spec.Lifecycle
	configured := lifecycle.Policy != "" || lifecycle.Validation != "" || lifecycle.Checkpoint != ""
	if !configured {
		return
	}
	if lifecycle.Policy != "" && lifecycle.Policy != "safe-resume" {
		v.add(Invalid, "spec.lifecycle.policy", "unsupported lifecycle policy %q", lifecycle.Policy)
	}
	if lifecycle.Validation != "" {
		v.validation("spec.lifecycle.validation", lifecycle.Validation)
	}
	if lifecycle.Checkpoint != "" {
		v.tool("spec.lifecycle.checkpoint", lifecycle.Checkpoint)
	}
	if lifecycle.Validation == "" {
		for i, phase := range v.w.Spec.Phases {
			if phase.Validation == "" {
				v.add(Invalid, fmt.Sprintf("spec.phases[%d].validation", i), "is required when spec.lifecycle.validation is not set")
			}
		}
	}
	if len(v.w.Spec.PhaseDefaults.Before) != 0 || len(v.w.Spec.PhaseDefaults.After) != 0 {
		v.add(Invalid, "spec.phaseDefaults", "cannot be combined with spec.lifecycle; use legacy lifecycle actions or the runtime-owned policy")
	}
	for i, phase := range v.w.Spec.Phases {
		if len(phase.After) != 0 {
			v.add(Invalid, fmt.Sprintf("spec.phases[%d].after", i), "cannot be combined with spec.lifecycle; use the runtime-owned lifecycle contract")
		}
	}
}

func (v validator) validationFailurePolicy(path string, validation Validation) {
	if validation.Repair != "" && validation.Repair != "none" {
		v.add(Invalid, path+".repair", "unsupported validation repair policy %q", validation.Repair)
	}
	if validation.Failure != "" && validation.Failure != "fail-workflow" {
		v.add(Invalid, path+".failure", "unsupported validation failure policy %q", validation.Failure)
	}
	policy := validation.OnFailure
	if policy.Exhausted != "" && policy.Exhausted != "fail-workflow" {
		v.add(Invalid, path+".onFailure.exhausted", "unsupported exhausted policy %q", policy.Exhausted)
	}
	switch policy.Strategy {
	case "":
		if policy.MaxRepairAttempts != 0 || policy.Repair.Actor != "" || policy.Repair.Reasoning != "" || policy.Repair.Prompt != "" || len(policy.Then) != 0 || policy.Exhausted != "" {
			v.add(Invalid, path+".onFailure", "requires strategy: repair-once when repair settings are declared")
		}
	case "repair-once":
		if policy.MaxRepairAttempts != 1 {
			v.add(Invalid, path+".onFailure.maxRepairAttempts", "repair-once requires exactly one repair attempt")
		}
		if policy.Repair.Actor == "" {
			v.add(Invalid, path+".onFailure.repair.actor", "is required for repair-once")
		} else {
			v.agent(path+".onFailure.repair.actor", policy.Repair.Actor)
		}
	default:
		v.add(Invalid, path+".onFailure.strategy", "unsupported validation failure strategy %q", policy.Strategy)
	}
}
func (v validator) toolUses(path string, uses []ToolUse) {
	for i, u := range uses {
		if u.Uses == "" {
			v.add(Invalid, fmt.Sprintf("%s[%d].uses", path, i), "is required")
		} else {
			v.tool(fmt.Sprintf("%s[%d].uses", path, i), u.Uses)
		}
		v.condition(fmt.Sprintf("%s[%d].if", path, i), u.If)
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
		v.condition(p+".if", a.If)
		if a.AssertProgress != nil && a.AssertProgress.Criterion != "" && !strings.Contains(a.AssertProgress.Criterion, "{{") && !v.criterion(a.AssertProgress.Criterion) {
			v.add(Invalid, p+".assertProgress.criterion", "unknown criterion %q", a.AssertProgress.Criterion)
		}
	}
}
func (v validator) condition(path, value string) {
	if value == "" {
		return
	}
	if err := validateTypedExpression(value, v.staticContext()); err != nil {
		v.add(Invalid, path, "invalid expression: %s", err)
	}
}
func (v validator) loop(path string, loop Loop) {
	if v.w.Spec.Progress.Selection.Strategy != "first-unchecked" {
		v.add(Invalid, "spec.progress.selection.strategy", "must be first-unchecked when a loop selects progress.next_unchecked")
	}
	if loop.While == "" {
		v.add(Invalid, path+".while", "is required")
	} else {
		v.condition(path+".while", loop.While)
	}
	if loop.MaxIterations == "" {
		v.add(Invalid, path+".maxIterations", "is required")
	} else {
		v.condition(path+".maxIterations", loop.MaxIterations)
	}
	if loop.Select != "{{ progress.next_unchecked }}" {
		v.add(Invalid, path+".select", "must be {{ progress.next_unchecked }}")
	}
	if len(loop.DispatchByCriterion) == 0 {
		v.add(Invalid, path+".dispatchByCriterion", "must dispatch at least one criterion")
	}
	for _, criterion := range sortedKeys(loop.DispatchByCriterion) {
		phase := loop.DispatchByCriterion[criterion]
		if !v.criterion(criterion) {
			v.add(Invalid, path+".dispatchByCriterion."+criterion, "unknown criterion %q", criterion)
		}
		v.phase(path+".dispatchByCriterion."+criterion, phase)
	}
	if loop.RequireUncheckedCountDelta >= 0 {
		v.add(Invalid, path+".requireUncheckedCountDelta", "must be negative")
	}
}
func (v validator) assertions(path string, as []Assertion) {
	for i, a := range as {
		v.assertion(fmt.Sprintf("%s[%d]", path, i), a)
	}
}

func (v validator) assertion(path string, a Assertion) {
	if a.Uses != "" {
		v.tool(path+".uses", a.Uses)
		return
	}
	switch a.Type {
	case "progress-empty", "workspace-integrity", "integrity-baseline-unchanged", "implementation-workspace-clean":
	default:
		v.add(Invalid, path+".type", "unsupported assertion type %q", a.Type)
	}
}

func (v validator) check(path string, c Check) {
	switch c.Type {
	case "git-repository":
	case "commands-exist":
		if len(c.Commands) == 0 {
			v.add(Invalid, path+".commands", "is required for commands-exist")
		}
	case "files-exist":
		if len(c.Paths) == 0 {
			v.add(Invalid, path+".paths", "is required for files-exist")
		}
	case "file-contains":
		if c.Path == "" {
			v.add(Invalid, path+".path", "is required for file-contains")
		}
	case "git-object-exists":
		if c.Object == "" {
			v.add(Invalid, path+".object", "is required for git-object-exists")
		}
	case "git-ancestor":
		if c.Ancestor == "" {
			v.add(Invalid, path+".ancestor", "is required for git-ancestor")
		}
		if c.Descendant == "" {
			v.add(Invalid, path+".descendant", "is required for git-ancestor")
		}
	case "git-lineage":
	case "git-current-branch-equals":
		if c.Expected == "" {
			v.add(Invalid, path+".expected", "is required for git-current-branch-equals")
		}
	case "workspace-integrity":
		if c.Policy != "" && c.Policy != "spec.workspace.mutationPolicy.integrity" {
			v.add(Invalid, path+".policy", "unsupported workspace integrity policy %q", c.Policy)
		}
	default:
		v.add(Invalid, path+".type", "unsupported precondition type %q", c.Type)
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

func (v validator) criterionID(id string) bool {
	for _, c := range v.w.Spec.Progress.Criteria {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (v validator) markdownTransition(path string, transition MarkdownTransition) {
	if transition.Path == "" {
		v.add(Invalid, path+".path", "is required")
	}
	switch transition.Type {
	case "markdown-checklist", "markdown-index":
		if transition.Item == "" {
			v.add(Invalid, path+".item", "is required")
		}
		if transition.State != "checked" && transition.State != "unchecked" {
			v.add(Invalid, path+".state", "must be checked or unchecked")
		}
	case "markdown-status":
		if transition.Label == "" {
			v.add(Invalid, path+".label", "is required")
		}
		if transition.From == "" || transition.To == "" {
			v.add(Invalid, path, "status transitions require from and to")
		} else if transition.From == transition.To {
			v.add(Invalid, path, "status transition from and to must differ")
		}
	default:
		v.add(Invalid, path+".type", "unsupported Markdown transition type %q", transition.Type)
	}
}

// expressions parses every expression before a repository is opened. Runtime
// values (state, progress, and environment) remain unresolved, but unknown
// roots, malformed operators, and unsupported calls fail closed here.
func (v validator) expressions() {
	visitStrings(reflect.ValueOf(v.w), "", func(path, value string) {
		if !strings.Contains(value, "{{") && !strings.Contains(value, "}}") {
			return
		}
		if err := validateTemplate(value, v.staticContext()); err != nil {
			v.add(Invalid, path, "invalid expression: %s", err)
		}
	})
}

func (v validator) staticContext() StaticContext {
	return StaticContext{Parameters: v.w.Spec.Parameters, Paths: v.w.Spec.Paths}
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

func (v validator) runtimeSurface() {
	if backend := v.w.Spec.State.Backend; backend != "" && backend != "git-dir" {
		v.add(Unsupported, "spec.state.backend", "state backend %q is not implemented by this runtime", backend)
	}
	if vcs := v.w.Spec.Workspace.VCS; vcs != "" && vcs != "git" {
		v.add(Unsupported, "spec.workspace.vcs", "workspace VCS %q is not implemented by this runtime", vcs)
	}
	if cleanup := v.w.Spec.Temp.Cleanup; cleanup != "" && cleanup != "on-exit" {
		v.add(Unsupported, "spec.temp.cleanup", "temp cleanup policy %q is not implemented by this runtime", cleanup)
	}
	if sourceType := v.w.Spec.Progress.Source.Type; sourceType != "" && sourceType != "markdown-checklist" {
		v.add(Unsupported, "spec.progress.source.type", "progress source type %q is not implemented by this runtime", sourceType)
	}
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
		case "shell", "workspace-policy", "git-checkpoint", "file-regex", "markdown-checklist-progress":
		default:
			v.add(Unsupported, "spec.tools."+name+".type", "tool type %q is not implemented by this runtime", t.Type)
		}
	}
}
