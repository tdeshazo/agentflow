package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

type Engine struct {
	Workflow   *workflow.Workflow
	Repo       gitstate.Repo
	Store      gitstate.Store
	Providers  map[string]provider.Provider
	Parameters map[string]any
	In         io.Reader
	Out        io.Writer

	lastFailure string
	phase       *workflow.Phase
}

type ActivePhase struct {
	PhaseID           string         `json:"phase_id"`
	StartCommit       string         `json:"phase_start_commit"`
	CheckpointCommit  string         `json:"checkpoint_commit,omitempty"`
	CheckpointPending bool           `json:"checkpoint_pending,omitempty"`
	UncheckedBefore   int            `json:"unchecked_count_before"`
	CheckedBefore     []string       `json:"checked_before"`
	RepairAttempts    map[string]int `json:"repair_attempts,omitempty"`
}

type IntegrityBaseline map[string]string

var errFlowStoppedSuccessfully = errors.New("workflow stopped successfully")

type Options struct {
	RepoRoot  string
	Overrides map[string]string
}

func New(w *workflow.Workflow, providers map[string]provider.Provider, opts Options) (*Engine, error) {
	params, err := resolveParameters(w, opts.Overrides)
	if err != nil {
		return nil, err
	}
	ctx := workflow.Context{Metadata: w.Metadata, Parameters: params, Paths: w.Spec.Paths, WorkflowFile: w.File}
	root := w.Spec.Workspace.Root
	if opts.RepoRoot != "" {
		root = opts.RepoRoot
	}
	if root == "" {
		root = "{{ parameters.repo_root }}"
	}
	root, err = ctx.Expand(root)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	repo := gitstate.Repo{Root: abs}
	return &Engine{Workflow: w, Repo: repo, Store: gitstate.NewStore(repo, w.Metadata.Name), Providers: providers, Parameters: params, In: os.Stdin, Out: os.Stdout}, nil
}

func resolveParameters(w *workflow.Workflow, overrides map[string]string) (map[string]any, error) {
	for name := range overrides {
		if _, ok := w.Spec.Parameters[name]; !ok {
			return nil, fmt.Errorf("unknown parameter override %q", name)
		}
	}
	out := map[string]any{}
	resolving := map[string]bool{}
	var resolve func(string) (any, error)
	resolve = func(name string) (any, error) {
		if value, ok := out[name]; ok {
			return value, nil
		}
		if resolving[name] {
			return nil, fmt.Errorf("parameter %s: cyclic default reference", name)
		}
		p, ok := w.Spec.Parameters[name]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
		resolving[name] = true
		defer delete(resolving, name)
		var value any = p.Default
		if p.Env != "" {
			if env, ok := os.LookupEnv(p.Env); ok {
				value = env
			}
		}
		if override, ok := overrides[name]; ok {
			value = override
		}
		if text, ok := value.(string); ok && strings.Contains(text, "{{") {
			dependencies, err := workflow.ParameterReferences(text)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", name, err)
			}
			for _, dependency := range dependencies {
				if dependency == name {
					return nil, fmt.Errorf("parameter %s: cyclic default reference", name)
				}
				if _, err := resolve(dependency); err != nil {
					return nil, err
				}
			}
			context := workflow.Context{Metadata: w.Metadata, Parameters: out, Paths: w.Spec.Paths, WorkflowFile: w.File}
			if strings.TrimSpace(text) == text && strings.HasPrefix(text, "{{") && strings.HasSuffix(text, "}}") {
				value, err = context.EvalTemplate(text)
			} else {
				value, err = context.Expand(text)
			}
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", name, err)
			}
		}
		parsed, err := coerce(p.Type, value)
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", name, err)
		}
		out[name] = parsed
		return parsed, nil
	}
	for name := range w.Spec.Parameters {
		if _, err := resolve(name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func coerce(kind string, v any) (any, error) {
	switch kind {
	case "", "string", "path":
		if s, ok := v.(string); ok {
			return s, nil
		}
		return nil, fmt.Errorf("must be a string, got %T", v)
	case "boolean":
		switch value := v.(type) {
		case bool:
			return value, nil
		case string:
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("must be boolean (true or false): %w", err)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("must be boolean, got %T", v)
		}
	case "integer":
		switch value := v.(type) {
		case int:
			return value, nil
		case string:
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("must be integer: %w", err)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("must be integer, got %T", v)
		}
	default:
		return nil, fmt.Errorf("unsupported parameter type %q", kind)
	}
}

func (e *Engine) Run(ctx context.Context) error {
	if !e.Repo.IsRepository() {
		return fmt.Errorf("%s is not a Git repository", e.Repo.Root)
	}
	if err := e.runBasicPreconditions(); err != nil {
		return err
	}
	reset, err := e.resetRequested()
	if err != nil {
		return fmt.Errorf("reset condition: %w", err)
	}
	if reset {
		if err := e.Reset(); err != nil {
			return err
		}
	}
	if err := e.initializeOrResumeState(); err != nil {
		return err
	}
	if err := e.runStatePreconditions(); err != nil {
		return err
	}

	complete, completeSHA, err := e.validCommitMarker("complete")
	if err != nil {
		return err
	}
	if complete {
		fmt.Fprintf(e.Out, "Workflow %s already complete at %s\n", e.Workflow.Metadata.Name, completeSHA)
		return nil
	}
	var active ActivePhase
	activeExists, err := e.Store.GetJSON("active", &active)
	if err != nil {
		return err
	}
	if activeExists {
		if !e.resumeEnabled() {
			return fmt.Errorf("workflow has an interrupted active phase but resume is disabled")
		}
		// Resume is a runtime safety invariant. A flow-level recover action is
		// still accepted for clarity, but an active record must never be bypassed
		// merely because a workflow places that action after a phase step.
		if err := e.recoverActive(ctx); err != nil {
			return err
		}
	}
	for _, step := range e.Workflow.Spec.Flow {
		if err := e.runFlowStep(ctx, step); err != nil {
			if errors.Is(err, errFlowStoppedSuccessfully) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (e *Engine) resumeEnabled() bool {
	if e.Workflow.Spec.State.Resume.Enabled == nil {
		return true
	}
	return *e.Workflow.Spec.State.Resume.Enabled
}

func (e *Engine) runFlowStep(ctx context.Context, step workflow.FlowStep) error {
	if step.If != "" {
		ok, err := e.bool(nil, step.If)
		if err != nil {
			return fmt.Errorf("flow %s condition: %w", step.ID, err)
		}
		if !ok {
			return nil
		}
	}
	// The action order is fixed rather than inherited from YAML map order. It
	// permits a small compound step (for example validate then checkpoint)
	// without turning flow steps into an imperative scripting language.
	if step.Recover != "" {
		if err := e.recoverActive(ctx); err != nil {
			return err
		}
	}
	if step.Phase != "" {
		if err := e.runPhase(ctx, step.Phase); err != nil {
			return err
		}
	}
	if step.Loop != nil {
		if err := e.runLoop(ctx, *step.Loop); err != nil {
			return err
		}
	}
	if step.Validate != "" {
		if err := e.runValidation(ctx, step.Validate, nil); err != nil {
			return err
		}
	}
	if step.Assert != nil {
		if err := e.runFlowAssertion(*step.Assert); err != nil {
			return err
		}
	}
	if step.Checkpoint != "" {
		if err := e.checkpoint(step.Label, nil); err != nil {
			return err
		}
	}
	if step.Human != "" {
		if err := e.runHuman(step.Human); err != nil {
			return err
		}
	}
	if step.Complete != "" {
		if err := e.runCompletion(ctx, step.Complete); err != nil {
			return err
		}
	}
	for _, action := range step.Then {
		if action.Report != "" {
			message, err := e.context(nil).Expand(action.Report)
			if err != nil {
				return err
			}
			fmt.Fprintln(e.Out, message)
		}
		if action.Stop != "" {
			if action.Stop == "success" {
				return errFlowStoppedSuccessfully
			}
			return fmt.Errorf("workflow stopped: %s", action.Stop)
		}
	}
	return nil
}

func (e *Engine) Reset() error {
	if !e.Repo.IsRepository() {
		return fmt.Errorf("%s is not a Git repository", e.Repo.Root)
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) != 0 {
		return fmt.Errorf("reset requires clean implementation workspace; dirty: %s", strings.Join(dirty, ", "))
	}
	return e.Store.Reset()
}

func (e *Engine) Status() error {
	base, bok, err := e.Store.Resolve("base")
	if err != nil {
		return err
	}
	complete, cok, err := e.Store.Resolve("complete")
	if err != nil {
		return err
	}
	var branch string
	_, _ = e.Store.GetJSON("branch", &branch)
	var active ActivePhase
	aok, _ := e.Store.GetJSON("active", &active)
	fmt.Fprintf(e.Out, "workflow: %s\nrepo: %s\ninitialized: %v\n", e.Workflow.Metadata.Name, e.Repo.Root, bok)
	if bok {
		fmt.Fprintf(e.Out, "base: %s\nbranch: %s\n", base, branch)
	}
	if aok {
		fmt.Fprintf(e.Out, "active_phase: %s @ %s\n", active.PhaseID, active.StartCommit)
	}
	fmt.Fprintf(e.Out, "complete: %v", cok)
	if cok {
		fmt.Fprintf(e.Out, " @ %s", complete)
	}
	fmt.Fprintln(e.Out)
	return nil
}

func (e *Engine) runBasicPreconditions() error {
	for _, c := range e.Workflow.Spec.Preconditions {
		if c.When != "" {
			continue
		}
		if err := e.runCheck(c); err != nil {
			return fmt.Errorf("precondition %s: %w", c.ID, err)
		}
	}
	return nil
}

func (e *Engine) runStatePreconditions() error {
	for _, c := range e.Workflow.Spec.Preconditions {
		if c.When == "" {
			continue
		}
		ok, err := e.bool(nil, c.When)
		if err != nil {
			return fmt.Errorf("precondition %s condition: %w", c.ID, err)
		}
		if !ok {
			continue
		}
		if err := e.runCheck(c); err != nil {
			return fmt.Errorf("precondition %s: %w", c.ID, err)
		}
	}
	return nil
}

func (e *Engine) runCheck(c workflow.Check) error {
	x := e.context(nil)
	switch c.Type {
	case "git-repository":
		if !e.Repo.IsRepository() {
			return errors.New("not a git repository")
		}
	case "commands-exist":
		for _, cmd := range c.Commands {
			if _, err := exec.LookPath(cmd); err != nil {
				return fmt.Errorf("command %s not found", cmd)
			}
		}
	case "files-exist":
		for _, p := range c.Paths {
			p, err := x.Expand(p)
			if err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(e.Repo.Root, p)); err != nil {
				return err
			}
		}
	case "file-contains":
		p, err := x.Expand(c.Path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(e.Repo.Root, p))
		if err != nil {
			return err
		}
		text, err := x.Expand(c.Text)
		if err != nil {
			return err
		}
		if !bytes.Contains(b, []byte(text)) {
			return fmt.Errorf("%s does not contain %q", p, text)
		}
	case "git-object-exists":
		obj, err := x.Expand(c.Object)
		if err != nil {
			return err
		}
		if !e.Repo.ObjectExists(obj) {
			return fmt.Errorf("git object %s does not exist", obj)
		}
	case "git-ancestor":
		a, err := x.Expand(c.Ancestor)
		if err != nil {
			return err
		}
		d, err := x.Expand(c.Descendant)
		if err != nil {
			return err
		}
		if !e.Repo.IsAncestor(a, d) {
			return fmt.Errorf("%s is not ancestor of %s", a, d)
		}
	case "git-lineage":
		base, err := x.Expand(c.Base)
		if err != nil {
			return err
		}
		if base == "" {
			base, err = x.Expand(c.Ancestor)
			if err != nil {
				return err
			}
		}
		if c.RequireAncestorOfHead && base != "" {
			if !e.Repo.IsAncestor(base, "HEAD") {
				return fmt.Errorf("%s is not ancestor of HEAD", base)
			}
		}
		if c.RequireBranch != "" {
			want, err := x.Expand(c.RequireBranch)
			if err != nil {
				return err
			}
			got, err := e.Repo.Branch()
			if err != nil {
				return err
			}
			if got != want {
				return fmt.Errorf("branch %q != %q", got, want)
			}
		}
	case "git-current-branch-equals":
		want, err := x.Expand(c.Expected)
		if err != nil {
			return err
		}
		got, err := e.Repo.Branch()
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("branch %q != %q", got, want)
		}
	case "workspace-integrity":
		return e.assertIntegrity()
	default:
		return fmt.Errorf("unsupported precondition type %q", c.Type)
	}
	return nil
}

func (e *Engine) initializeOrResumeState() error {
	base, ok, err := e.Store.Resolve("base")
	if err != nil {
		return err
	}
	if !ok {
		return e.initializeState()
	}
	if !e.Repo.ObjectExists(base + "^{commit}") {
		return fmt.Errorf("saved base no longer exists: %s", base)
	}
	if !e.Repo.IsAncestor(base, "HEAD") {
		return fmt.Errorf("HEAD no longer descends from workflow base %s", base)
	}
	var branch string
	branchOK, err := e.Store.GetJSON("branch", &branch)
	if err != nil {
		return err
	}
	var integrity IntegrityBaseline
	integrityOK, err := e.Store.GetJSON("integrity", &integrity)
	if err != nil {
		return err
	}
	if !branchOK || !integrityOK {
		if err := e.resetInterruptedInitialization(); err != nil {
			return err
		}
		return e.initializeState()
	}
	current, err := e.Repo.Branch()
	if err != nil {
		return fmt.Errorf("workflow requires its initialized named branch; detached HEAD is not supported")
	}
	if current != branch {
		return fmt.Errorf("current branch %q differs from workflow branch %q", current, branch)
	}
	return e.assertIntegrity()
}

func (e *Engine) initializeState() error {
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("first run requires clean implementation workspace: %s", strings.Join(dirty, ", "))
	}
	branch, err := e.Repo.Branch()
	if err != nil || branch == "" {
		return fmt.Errorf("workflow requires a named branch")
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if err := e.Store.SetCommit("base", head); err != nil {
		return err
	}
	if err := e.Store.SetJSON("branch", branch); err != nil {
		return err
	}
	baseline, err := e.computeIntegrity()
	if err != nil {
		return err
	}
	return e.Store.SetJSON("integrity", baseline)
}

func (e *Engine) resetInterruptedInitialization() error {
	names, err := e.Store.Names()
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != "base" && name != "branch" && name != "integrity" {
			return fmt.Errorf("workflow state is incomplete and contains execution evidence %q", name)
		}
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("workflow initialization is incomplete and implementation workspace is dirty: %s", strings.Join(dirty, ", "))
	}
	return e.Store.Reset()
}

func (e *Engine) context(p *workflow.Phase) workflow.Context {
	x := e.contextWithoutProgress(p)
	progress, _ := e.progressContext()
	x.Progress = progress
	return x
}

func (e *Engine) bool(p *workflow.Phase, expression string) (bool, error) {
	x := e.contextWithoutProgress(p)
	progress, err := e.progressContext()
	if err != nil {
		return false, err
	}
	x.Progress = progress
	return x.Bool(expression)
}

func (e *Engine) integer(p *workflow.Phase, expression string) (int, error) {
	x := e.contextWithoutProgress(p)
	progress, err := e.progressContext()
	if err != nil {
		return 0, err
	}
	x.Progress = progress
	return x.Int(expression)
}

func (e *Engine) contextWithoutProgress(p *workflow.Phase) workflow.Context {
	base, baseOK, _ := e.Store.Resolve("base")
	var branch string
	_, _ = e.Store.GetJSON("branch", &branch)
	complete, completeOK, _ := e.Store.Resolve("complete")
	var active ActivePhase
	activeOK, _ := e.Store.GetJSON("active", &active)
	head, _ := e.Repo.Head()
	state := map[string]any{
		"initialized":       baseOK,
		"base_commit":       base,
		"branch":            branch,
		"workflow_complete": map[string]any{"exists": completeOK, "value": complete},
		"active_phase": map[string]any{
			"exists":                 activeOK,
			"value":                  active.PhaseID,
			"phase_id":               active.PhaseID,
			"phase_start_commit":     active.StartCommit,
			"unchecked_count_before": active.UncheckedBefore,
		},
	}
	return workflow.Context{Metadata: e.Workflow.Metadata, Parameters: e.Parameters, Paths: e.Workflow.Spec.Paths, State: state, Phase: p, WorkflowFile: e.Workflow.File, FailureLog: e.lastFailure, HeadCommit: head}
}

func (e *Engine) resetRequested() (bool, error) {
	when := e.Workflow.Spec.State.Reset.When
	if when == "" {
		return false, nil
	}
	return e.bool(nil, when)
}
