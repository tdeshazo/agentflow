// Package engine provides the agentflow workflow execution runtime.
// It orchestrates workflow phases, manages durability through Git state,
// and coordinates with external providers to execute agent work.
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
	"sync/atomic"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/observability"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

// Engine orchestrates workflow execution, managing durability, phase lifecycle,
// and coordination with external providers.
type Engine struct {
	Workflow         *workflow.Workflow
	identityWorkflow *workflow.Workflow
	Repo             gitstate.Repo
	Store            gitstate.Store
	Providers        map[string]provider.Provider
	Parameters       map[string]any
	In               io.Reader
	Out              io.Writer
	// HumanGateInteractive overrides terminal detection for deterministic
	// callers and tests. A nil function uses the real input/output terminal
	// check, so pipes and programmatic readers retain the non-interactive
	// protocol.
	HumanGateInteractive HumanGateInteractivity

	lastFailure        string
	phase              *workflow.Phase
	invocationID       string
	tempDirectory      string
	parametersResolved bool
	logStore           *observability.LogStore
	outputBridge       *observability.OutputBridge
	outputRestore      func()
	detached           bool
}

// ActivePhase is the durable record of a phase's current execution state,
// including checkpoints, validation status, and repair attempts.
type ActivePhase struct {
	PhaseID     string `json:"phase_id"`
	StartCommit string `json:"phase_start_commit"`
	// ActorCompleted is durable evidence that the phase's primary actor
	// returned successfully. Until it is true, recovery must not let
	// deterministic validation substitute for the actor invocation.
	ActorCompleted          bool                `json:"actor_completed"`
	CheckpointCommit        string              `json:"checkpoint_commit,omitempty"`
	CheckpointPending       bool                `json:"checkpoint_pending,omitempty"`
	UncheckedBefore         int                 `json:"unchecked_count_before"`
	CheckedBefore           []string            `json:"checked_before"`
	CriteriaBefore          map[string]bool     `json:"criteria_before,omitempty"`
	ProgressItemsBefore     []ProgressItemState `json:"progress_items_before,omitempty"`
	TargetCriterionID       string              `json:"target_criterion_id,omitempty"`
	ProgressAdvancePending  bool                `json:"progress_advance_pending,omitempty"`
	ProgressAdvanced        bool                `json:"progress_advanced,omitempty"`
	BookkeepingPending      bool                `json:"bookkeeping_pending,omitempty"`
	BookkeepingApplied      bool                `json:"bookkeeping_applied,omitempty"`
	BookkeepingStateDigests map[string][]string `json:"bookkeeping_state_digests,omitempty"`
	RepairAttempts          map[string]int      `json:"repair_attempts,omitempty"`
	FailureKind             PhaseFailureKind    `json:"failure_kind,omitempty"`
	Validation              string              `json:"validation,omitempty"`
	ValidationError         string              `json:"validation_error,omitempty"`
	ValidationPassed        bool                `json:"validation_passed,omitempty"`
}

// ProgressItemState is the durable, ordered Markdown progress baseline used
// to prove that engine-owned acceptance changed only its declared target.
type ProgressItemState struct {
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

// PhaseFailureKind records the runtime classification of a pending acceptance
// failure without requiring recovery to infer authority from an error string.
type PhaseFailureKind string

const (
	PhaseFailureValidation PhaseFailureKind = "validation"
	PhaseFailureSafety     PhaseFailureKind = "safety"
)

// IntegrityBaseline is a map of paths to their expected hash values for integrity checking.
type IntegrityBaseline map[string]string

var errFlowStoppedSuccessfully = errors.New("workflow stopped successfully")
var invocationSequence uint64

// Options specifies configuration for creating a new Engine.
type Options struct {
	RepoRoot  string
	Overrides map[string]string
	// Detached enables child-process diagnostic capture. It does not change
	// workflow semantics or state authority.
	Detached bool
	// StateOnly constructs an engine for status or explicit reset. When the
	// repository root is supplied, it deliberately avoids resolving run
	// parameters so operators need not re-enter a task or secret just to inspect
	// or discard durable state.
	StateOnly bool
}

// New creates a new Engine for executing the given workflow with the provided providers.
// It resolves parameters, initializes Git state storage, and validates configuration.
func New(w *workflow.Workflow, providers map[string]provider.Provider, opts Options) (*Engine, error) {
	// Callers in Go may construct a Workflow directly, while file callers have
	// already validated the authored form. Normalize here as the final boundary
	// so execution always sees the same explicit contract as `plan --expanded`.
	authored := w
	normalized, err := workflow.NormalizeWorkflow(&workflow.Document{Workflow: authored})
	if err != nil {
		return nil, err
	}
	w = normalized.Workflow
	params := map[string]any{}
	parametersResolved := false
	if !opts.StateOnly {
		params, err = resolveParameters(w, opts.Overrides)
		if err != nil {
			return nil, err
		}
		parametersResolved = true
	} else if opts.RepoRoot == "" {
		rootTemplate := w.Spec.Workspace.Root
		if rootTemplate == "" {
			rootTemplate = "{{ parameters.repo_root }}"
		}
		rootParameters, err := workflow.ParameterReferences(rootTemplate)
		if err != nil {
			return nil, fmt.Errorf("workspace root: %w", err)
		}
		params, err = resolveParameterNames(w, opts.Overrides, rootParameters)
		if err != nil {
			return nil, err
		}
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
	e := &Engine{Workflow: w, identityWorkflow: authored, Repo: repo, Store: gitstate.NewStore(repo, w.Metadata.Name), Providers: providers, Parameters: params, In: os.Stdin, Out: os.Stdout, invocationID: fmt.Sprintf("%d", atomic.AddUint64(&invocationSequence, 1)), parametersResolved: parametersResolved, detached: opts.Detached}
	if !opts.StateOnly && w.Spec.Temp.Directory != "" {
		pattern, err := ctx.Expand(w.Spec.Temp.Directory)
		if err != nil {
			return nil, err
		}
		pattern = strings.ReplaceAll(pattern, "${TMPDIR:-/tmp}", os.TempDir())
		parent, prefix := filepath.Dir(pattern), filepath.Base(pattern)
		prefix = strings.TrimRight(prefix, "X")
		if prefix == "" {
			prefix = "agentflow-"
		}
		directory, err := os.MkdirTemp(parent, prefix)
		if err != nil {
			return nil, fmt.Errorf("create temp directory: %w", err)
		}
		e.tempDirectory = directory
	}
	return e, nil
}

func resolveParameters(w *workflow.Workflow, overrides map[string]string) (map[string]any, error) {
	return resolveParameterNames(w, overrides, nil)
}

// resolveParameterNames resolves only the parameters needed by a state-only
// operation to locate its repository. A nil name list resolves the complete
// run parameter set.
func resolveParameterNames(w *workflow.Workflow, overrides map[string]string, names []string) (map[string]any, error) {
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
	if names == nil {
		names = make([]string, 0, len(w.Spec.Parameters))
		for name := range w.Spec.Parameters {
			names = append(names, name)
		}
	}
	for _, name := range names {
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

// Run executes the workflow, orchestrating phases, managing durability, and coordinating
// with providers. It returns an error if the workflow fails.
func (e *Engine) Run(ctx context.Context) (runErr error) {
	if e.tempDirectory != "" && e.Workflow.Spec.Temp.Cleanup == "on-exit" {
		defer os.RemoveAll(e.tempDirectory)
	}
	if !e.Repo.IsRepository() {
		return fmt.Errorf("%s is not a Git repository", e.Repo.Root)
	}
	if err := e.startObservation(); err != nil {
		return err
	}
	defer func() {
		if e.logStore != nil {
			fields := map[string]string{"result": "success"}
			if runErr != nil {
				fields["result"] = "failure"
			}
			_ = e.logStore.Event("workflow_end", fields)
			if e.outputRestore != nil {
				e.outputRestore()
				e.outputRestore = nil
			}
			if e.outputBridge != nil {
				_ = e.outputBridge.Close()
				e.outputBridge = nil
			}
			_ = e.logStore.Close()
			e.logStore = nil
		}
		_ = e.finishObservation()
	}()
	e.logEvent("workflow_start", map[string]string{"workflow": e.Workflow.Metadata.Name})
	if _, active, err := e.Store.Resolve(e.activeRecord()); err == nil && active {
		e.logEvent("workflow_resume", map[string]string{"workflow": e.Workflow.Metadata.Name})
	}
	if err := e.runBasicPreconditions(); err != nil {
		return err
	}
	reset, err := e.resetRequested()
	if err != nil {
		return fmt.Errorf("reset condition: %w", err)
	}
	// A reset is the intentional escape hatch from a prior run identity. For
	// every other run, reject incompatible inputs before even basic checks can
	// consult or advance durable acceptance evidence.
	if !reset {
		if _, err := e.verifyStoredRunIdentity(); err != nil {
			return err
		}
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

	complete, completeSHA, err := e.validCommitMarker(e.workflowCompleteMarker())
	if err != nil {
		return err
	}
	if complete {
		if err := e.assertMutationBoundary(true, e.lifecycleConfigured()); err != nil {
			return fmt.Errorf("completed workflow is no longer safe to reuse: %w", err)
		}
		e.presenter().WorkflowAlreadyComplete(e.Workflow.Metadata.Name, completeSHA)
		return nil
	}
	var active ActivePhase
	activeExists, err := e.Store.GetJSON(e.activeRecord(), &active)
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

func (e *Engine) startObservation() error {
	logStore, err := observability.Open(e.Repo, e.Workflow.Metadata.Name)
	if err != nil {
		return err
	}
	descriptor := gitstate.NewDescriptor(e.Workflow.Metadata.Name, e.Workflow.File, gitstate.RecordNames{
		Base:                 e.baseRecord(),
		Branch:               e.branchRecord(),
		ActivePhase:          e.activeRecord(),
		WorkflowComplete:     e.workflowCompleteMarker(),
		CompletedPhasePrefix: e.Workflow.Spec.State.Records.CompletedPhases,
	})
	descriptor.Process = gitstate.CurrentProcessMetadata()
	if err := descriptor.Validate(e.Workflow.Metadata.Name); err != nil {
		_ = logStore.Close()
		return fmt.Errorf("observability descriptor: %w", err)
	}
	if err := e.Store.SetJSON(gitstate.DescriptorRecord, descriptor); err != nil {
		_ = logStore.Close()
		return fmt.Errorf("persist observability descriptor: %w", err)
	}
	e.logStore = logStore
	if e.detached {
		bridge, bridgeErr := observability.NewOutputBridge(logStore)
		if bridgeErr != nil {
			_ = logStore.Close()
			e.logStore = nil
			return fmt.Errorf("capture detached output: %w", bridgeErr)
		}
		oldStdout, oldStderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = bridge.Stdout(), bridge.Stderr()
		e.outputRestore = func() {
			os.Stdout, os.Stderr = oldStdout, oldStderr
		}
		e.outputBridge = bridge
		e.Out = bridge.Stdout()
	}
	return nil
}

func (e *Engine) finishObservation() error {
	var descriptor gitstate.Descriptor
	ok, err := e.Store.GetJSON(gitstate.DescriptorRecord, &descriptor)
	if err != nil || !ok {
		return err
	}
	// A second invocation may have replaced the descriptor while this process
	// was running. Never clear another process's verified liveness metadata.
	if descriptor.Process != nil {
		current := gitstate.CurrentProcessMetadata()
		if current == nil || current.PID != descriptor.Process.PID || current.Start != descriptor.Process.Start {
			return nil
		}
	}
	descriptor.Process = nil
	return e.Store.SetJSON(gitstate.DescriptorRecord, descriptor)
}

func (e *Engine) logEvent(kind string, fields map[string]string) {
	if e.logStore != nil {
		_ = e.logStore.Event(kind, fields)
	}
}

func (e *Engine) workflowCompleteMarker() string {
	if e.Workflow.Spec.State.Records.WorkflowComplete != "" {
		return e.Workflow.Spec.State.Records.WorkflowComplete
	}
	return "complete"
}

func configuredRecord(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (e *Engine) baseRecord() string {
	return configuredRecord(e.Workflow.Spec.State.Records.BaseCommit, "base")
}
func (e *Engine) branchRecord() string {
	return configuredRecord(e.Workflow.Spec.State.Records.Branch, "branch")
}
func (e *Engine) activeRecord() string {
	return configuredRecord(e.Workflow.Spec.State.Records.ActivePhase, "active")
}
func (e *Engine) integrityRecord() string   { return "integrity" }
func (e *Engine) runIdentityRecord() string { return "run-identity" }

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
		if err := e.runHuman(ctx, step.Human); err != nil {
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
			e.presenter().Notice(clioutput.RolePlain, "%s", message)
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

func (e *Engine) presenter() clioutput.Presenter {
	if e.detached {
		return clioutput.NewPresenterWithPresentation(e.Out, clioutput.PresentationRaw)
	}
	return clioutput.NewPresenter(e.Out)
}

// Reset clears the durable workflow state, allowing the workflow to be re-executed.
// It validates that workspace conditions are met before clearing state.
func (e *Engine) Reset() error {
	if !e.Repo.IsRepository() {
		return fmt.Errorf("%s is not a Git repository", e.Repo.Root)
	}
	if e.Workflow.Spec.State.Reset.RequireCleanWorkspace || e.Workflow.Spec.State.Reset.RequireCleanImplementationWorkspace || e.Workflow.Spec.State.Reset.When != "" {
		dirty, err := e.implementationDirtyFiles()
		if err != nil {
			return err
		}
		if len(dirty) != 0 {
			return fmt.Errorf("reset requires clean implementation workspace; dirty: %s", strings.Join(dirty, ", "))
		}
	}
	return e.Store.Reset()
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
	identityOK, err := e.verifyStoredRunIdentity()
	if err != nil {
		return err
	}
	base, ok, err := e.Store.Resolve(e.baseRecord())
	if err != nil {
		return err
	}
	if !ok {
		if identityOK {
			return fmt.Errorf("workflow state is corrupt: run identity exists without initialized base state")
		}
		names, err := e.Store.Names()
		if err != nil {
			return err
		}
		if len(names) != 0 && !(len(names) == 1 && names[0] == gitstate.DescriptorRecord) {
			if err := e.resetInterruptedInitialization(); err != nil {
				return err
			}
		}
		return e.initializeState()
	}
	requireBaseAncestor := e.Workflow.Spec.State.Lineage.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.State.Resume.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireBaseIsAncestorOfHead
	if (e.Workflow.Spec.State.Lineage.RequireBaseCommitExists || requireBaseAncestor) && !e.Repo.ObjectExists(base+"^{commit}") {
		return fmt.Errorf("saved base no longer exists: %s", base)
	}
	if requireBaseAncestor && !e.Repo.IsAncestor(base, "HEAD") {
		return fmt.Errorf("HEAD no longer descends from workflow base %s", base)
	}
	var branch string
	branchOK, err := e.Store.GetJSON(e.branchRecord(), &branch)
	if err != nil {
		return err
	}
	var integrity IntegrityBaseline
	integrityOK, err := e.Store.GetJSON(e.integrityRecord(), &integrity)
	if err != nil {
		return err
	}
	if !branchOK || !integrityOK || !identityOK {
		if err := e.resetInterruptedInitialization(); err != nil {
			return err
		}
		return e.initializeState()
	}
	if e.Workflow.Spec.State.Lineage.RequireSameNamedBranch || e.Workflow.Spec.State.Resume.RequireSameBranch || e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireSameBranchAsState {
		current, err := e.Repo.Branch()
		if err != nil {
			return fmt.Errorf("workflow requires its initialized named branch; detached HEAD is not supported")
		}
		if current != branch {
			return fmt.Errorf("current branch %q differs from workflow branch %q", current, branch)
		}
	}
	return e.assertIntegrity()
}

func (e *Engine) initializeState() error {
	if e.Workflow.Spec.State.Initialize.RequireCleanWorkspace || e.Workflow.Spec.State.Initialize.RequireCleanImplementationWorkspace || e.Workflow.Spec.Workspace.Cleanliness.BeforeFirstRun == "required" {
		dirty, err := e.implementationDirtyFiles()
		if err != nil {
			return err
		}
		if len(dirty) > 0 {
			return fmt.Errorf("first run requires clean implementation workspace: %s", strings.Join(dirty, ", "))
		}
	}
	branch, err := e.Repo.Branch()
	if (e.Workflow.Spec.State.Initialize.RequireNamedBranch || e.Workflow.Spec.State.Lineage.RequireSameNamedBranch || e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireSameBranchAsState) && (err != nil || branch == "") {
		return fmt.Errorf("workflow requires a named branch")
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if err := e.Store.SetCommit(e.baseRecord(), head); err != nil {
		return err
	}
	if err := e.Store.SetJSON(e.branchRecord(), branch); err != nil {
		return err
	}
	baseline, err := e.computeIntegrity()
	if err != nil {
		return err
	}
	if err := e.Store.SetJSON(e.integrityRecord(), baseline); err != nil {
		return err
	}
	for id, hash := range baseline {
		if record := e.Workflow.Spec.State.Records.Integrity[id]; record != "" {
			if err := e.Store.SetJSON(record, hash); err != nil {
				return err
			}
		}
	}
	identity, err := e.expectedRunIdentity()
	if err != nil {
		return err
	}
	// Write this last. Until then a restart treats the records as an
	// interrupted initialization rather than trusting a partially captured run.
	if err := e.Store.SetJSON(e.runIdentityRecord(), identity); err != nil {
		return fmt.Errorf("persist run identity: %w", err)
	}
	return nil
}

func (e *Engine) resetInterruptedInitialization() error {
	names, err := e.Store.Names()
	if err != nil {
		return err
	}
	allowed := map[string]bool{e.baseRecord(): true, e.branchRecord(): true, e.integrityRecord(): true, e.runIdentityRecord(): true, gitstate.DescriptorRecord: true}
	for _, record := range e.Workflow.Spec.State.Records.Integrity {
		allowed[record] = true
	}
	for _, name := range names {
		if !allowed[name] {
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
	base, baseOK, _ := e.Store.Resolve(e.baseRecord())
	var branch string
	_, _ = e.Store.GetJSON(e.branchRecord(), &branch)
	complete, completeOK, _ := e.Store.Resolve(e.workflowCompleteMarker())
	var active ActivePhase
	activeOK, _ := e.Store.GetJSON(e.activeRecord(), &active)
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
	for name, value := range stateRecordValues(e.Workflow.Spec.State.Records) {
		if _, reserved := state[name]; !reserved {
			state[name] = value
		}
	}
	return workflow.Context{Metadata: e.Workflow.Metadata, Parameters: e.Parameters, Paths: e.Workflow.Spec.Paths, State: state, Phase: p, WorkflowFile: e.Workflow.File, FailureLog: e.lastFailure, HeadCommit: head, InvocationID: e.invocationID, TempDirectory: e.tempDirectory}
}

func stateRecordValues(records workflow.StateRecords) map[string]any {
	return map[string]any{
		"base_commit":             records.BaseCommit,
		"branch":                  records.Branch,
		"active_phase":            records.ActivePhase,
		"completed_phase_pattern": records.CompletedPhasePattern,
		"completed_phases":        records.CompletedPhases,
		"manual_confirmation":     records.ManualConfirmation,
		"human_verification":      records.HumanVerification,
		"workflow_complete":       records.WorkflowComplete,
	}
}

func (e *Engine) recordName(template string, p *workflow.Phase) (string, error) {
	if template == "" {
		return "", nil
	}
	name, err := e.context(p).Expand(template)
	if err != nil {
		return "", err
	}
	// The state.* record aliases are names, while state.active_phase and
	// state.workflow_complete are structured runtime values. Resolve the two
	// structured aliases explicitly when used as marker destinations.
	trimmed := strings.TrimSpace(template)
	if trimmed == "{{ state.active_phase }}" {
		return e.Workflow.Spec.State.Records.ActivePhase, nil
	}
	if trimmed == "{{ state.workflow_complete }}" {
		return e.Workflow.Spec.State.Records.WorkflowComplete, nil
	}
	return strings.TrimPrefix(name, "/"), nil
}

func (e *Engine) resetRequested() (bool, error) {
	when := e.Workflow.Spec.State.Reset.When
	if when == "" {
		return false, nil
	}
	return e.bool(nil, when)
}
