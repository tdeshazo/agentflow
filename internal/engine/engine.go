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
	PhaseID         string `json:"phase_id"`
	StartCommit     string `json:"phase_start_commit"`
	UncheckedBefore int    `json:"unchecked_count_before"`
}

type IntegrityBaseline map[string]string

type Options struct {
	RepoRoot  string
	Overrides map[string]string
}

func New(w *workflow.Workflow, providers map[string]provider.Provider, opts Options) (*Engine, error) {
	params, err := resolveParameters(w, opts.Overrides)
	if err != nil {
		return nil, err
	}
	if opts.RepoRoot != "" {
		params["repo_root"] = opts.RepoRoot
	}
	ctx := workflow.Context{Metadata: w.Metadata, Parameters: params, Paths: w.Spec.Paths, WorkflowFile: w.File}
	root := w.Spec.Workspace.Root
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
	out := map[string]any{}
	for name, p := range w.Spec.Parameters {
		var v any = p.Default
		if p.Env != "" {
			if s, ok := os.LookupEnv(p.Env); ok {
				v = s
			}
		}
		if s, ok := overrides[name]; ok {
			v = s
		}
		if str, ok := v.(string); ok && strings.Contains(str, "{{") {
			expanded, err := (workflow.Context{Metadata: w.Metadata, Parameters: out, Paths: w.Spec.Paths, WorkflowFile: w.File}).Expand(str)
			if err != nil {
				return nil, fmt.Errorf("parameter %s: %w", name, err)
			}
			v = expanded
		}
		parsed, err := coerce(p.Type, v)
		if err != nil {
			return nil, fmt.Errorf("parameter %s: %w", name, err)
		}
		out[name] = parsed
	}
	for k, v := range overrides {
		if _, ok := w.Spec.Parameters[k]; !ok {
			return nil, fmt.Errorf("unknown parameter override %q", k)
		}
		_ = v
	}
	return out, nil
}

func coerce(kind string, v any) (any, error) {
	s := fmt.Sprint(v)
	switch kind {
	case "", "string", "path":
		return s, nil
	case "boolean":
		return strconv.ParseBool(s)
	case "integer":
		return strconv.Atoi(s)
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
	if e.resetRequested() {
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
	if err := e.recoverActive(ctx); err != nil {
		return err
	}

	for _, step := range e.Workflow.Spec.Flow {
		if step.If != "" {
			ok, err := e.context(nil).Bool(step.If)
			if err != nil {
				return fmt.Errorf("flow %s condition: %w", step.ID, err)
			}
			if !ok {
				continue
			}
			for _, a := range step.Then {
				if a.Report != "" {
					msg, _ := e.context(nil).Expand(a.Report)
					fmt.Fprintln(e.Out, msg)
				}
				if a.Stop != "" {
					if a.Stop == "success" {
						return nil
					}
					return fmt.Errorf("workflow stopped: %s", a.Stop)
				}
			}
		}
		switch {
		case step.Recover != "":
			if err := e.recoverActive(ctx); err != nil {
				return err
			}
		case step.Phase != "":
			if err := e.runPhase(ctx, step.Phase); err != nil {
				return err
			}
		case step.Validate != "":
			if err := e.runValidation(ctx, step.Validate, nil); err != nil {
				return err
			}
		case step.Checkpoint != "":
			if err := e.checkpoint(step.Label, nil); err != nil {
				return err
			}
		case step.Human != "":
			if err := e.runHuman(step.Human); err != nil {
				return err
			}
		case step.Complete != "":
			if err := e.runCompletion(ctx, step.Complete); err != nil {
				return err
			}
		case step.Assert != nil:
			if err := e.runFlowAssertion(*step.Assert); err != nil {
				return err
			}
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
		if c.When != "" && strings.Contains(c.When, "state.") {
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
		if c.When == "" || !strings.Contains(c.When, "state.") {
			continue
		}
		ok, err := e.context(nil).Bool(c.When)
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
		if !bytes.Contains(b, []byte(c.Text)) {
			return fmt.Errorf("%s does not contain %q", p, c.Text)
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
		if err := e.Store.SetJSON("integrity", baseline); err != nil {
			return err
		}
		return nil
	}
	if !e.Repo.ObjectExists(base + "^{commit}") {
		return fmt.Errorf("saved base no longer exists: %s", base)
	}
	if !e.Repo.IsAncestor(base, "HEAD") {
		return fmt.Errorf("HEAD no longer descends from workflow base %s", base)
	}
	var branch string
	ok, err = e.Store.GetJSON("branch", &branch)
	if err != nil || !ok {
		return fmt.Errorf("saved branch missing")
	}
	current, err := e.Repo.Branch()
	if err != nil {
		return err
	}
	if current != branch {
		return fmt.Errorf("current branch %q differs from workflow branch %q", current, branch)
	}
	return e.assertIntegrity()
}

func (e *Engine) context(p *workflow.Phase) workflow.Context {
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
		"active_phase":      map[string]any{"exists": activeOK, "value": active.PhaseID},
	}
	return workflow.Context{Metadata: e.Workflow.Metadata, Parameters: e.Parameters, Paths: e.Workflow.Spec.Paths, State: state, Phase: p, WorkflowFile: e.Workflow.File, FailureLog: e.lastFailure, HeadCommit: head}
}

func (e *Engine) resetRequested() bool {
	for _, key := range []string{"reset_workflow_state", "reset_state"} {
		if v, ok := e.Parameters[key].(bool); ok && v {
			return true
		}
	}
	return false
}
