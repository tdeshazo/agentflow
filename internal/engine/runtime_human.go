package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

// HumanGateInteractivity determines whether a human gate should use its
// checklist-by-checklist terminal protocol for a particular input/output pair.
// The default is clioutput.IsInteractive, which requires both ends to be
// terminal-backed.
type HumanGateInteractivity = func(io.Reader, io.Writer) bool

func (e *Engine) runFlowAssertion(a workflow.Assertion) error {
	typeName := a.Type
	switch typeName {
	case "implementation-workspace-clean":
		d, err := e.implementationDirtyFiles()
		if err != nil {
			return err
		}
		if len(d) > 0 {
			return fmt.Errorf("implementation workspace is dirty: %s", strings.Join(d, ", "))
		}
		return nil
	case "progress-empty":
		n, err := e.uncheckedCount()
		if err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("progress contains %d unchecked items", n)
		}
		return nil
	default:
		return fmt.Errorf("unsupported flow assertion %q", typeName)
	}
}

func (e *Engine) runHuman(ctx context.Context, id string) (runErr error) {
	e.logEvent("human_gate_start", map[string]string{"gate": id})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("human_gate_end", map[string]string{"gate": id, "result": result})
	}()
	var gate *workflow.HumanGate
	for i := range e.Workflow.Spec.HumanGates {
		if e.Workflow.Spec.HumanGates[i].ID == id {
			gate = &e.Workflow.Spec.HumanGates[i]
			break
		}
	}
	if gate == nil {
		return fmt.Errorf("unknown human gate %q", id)
	}
	presenter := clioutput.NewPresenter(e.Out)
	record := "human/" + id
	if gate.IdempotentRecord != "" {
		var err error
		record, err = e.recordName(gate.IdempotentRecord, nil)
		if err != nil {
			return err
		}
	}
	if ok, _, err := e.validCommitMarker(record); err != nil {
		return err
	} else if ok {
		presenter.HumanGateAlreadyRecorded(id)
		return nil
	}
	for _, prerequisite := range gate.Requires {
		phase, err := e.phaseByID(prerequisite)
		if err != nil {
			return err
		}
		if ok, _, err := e.validCommitMarker(e.phaseMarkerName(phase)); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("human gate %s requires completed phase %q", id, prerequisite)
		}
	}
	for _, after := range gate.After {
		if after.Phase != "" {
			if err := e.runPhase(ctx, after.Phase); err != nil {
				return err
			}
		}
		if after.Validation != "" {
			if err := e.runValidation(ctx, after.Validation, nil); err != nil {
				return err
			}
		}
	}
	condition := gate.When
	if gate.If != "" {
		if condition != "" {
			return fmt.Errorf("human gate %s declares both when and if", id)
		}
		condition = gate.If
	}
	required, err := e.bool(nil, condition)
	if err != nil {
		return fmt.Errorf("human gate %s condition: %w", id, err)
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if !required {
		if gate.Skip.AllowedWhen != "" {
			allowed, err := e.bool(nil, gate.Skip.AllowedWhen)
			if err != nil {
				return fmt.Errorf("human gate %s skip condition: %w", id, err)
			}
			if !allowed {
				return fmt.Errorf("human gate %s is not required but its skip is not allowed", id)
			}
		}
		if gate.Skip.Warning != "" {
			presenter.Notice(clioutput.RoleWarning, "WARNING: %s", gate.Skip.Warning)
		}
		if gate.Skip.Record != "" {
			resolved, err := e.recordName(gate.Skip.Record, nil)
			if err != nil {
				return err
			}
			if resolved != "" {
				record = resolved
			}
		}
		if gate.Skip.Evidence.Record != "" {
			resolved, err := e.recordName(gate.Skip.Evidence.Record, nil)
			if err != nil {
				return err
			}
			if resolved != "" {
				record = resolved
			}
		}
		return e.persistHumanEvidence(gate, record, head)
	}

	presenter.Separator()
	presenter.Rule(fmt.Sprintf("Human verification: %s", id))
	presenter.TextLine("%s", gate.Instructions)

	reader := bufio.NewReader(e.In)
	if e.humanGateIsInteractive() {
		for i, item := range gate.Checklist {
			presenter.Prompt("%d. %s [y/N]: ", i+1, item.Text)
			answer, err := readHumanLine(reader)
			if err != nil {
				return err
			}
			switch strings.ToLower(answer) {
			case "y", "yes":
				// Continue to the next required check.
			default:
				if item.ID != "" {
					return fmt.Errorf("human gate %s checklist item %d not confirmed: %s", id, i+1, item.ID)
				}
				return fmt.Errorf("human gate %s checklist item %d not confirmed: %s", id, i+1, item.Text)
			}
		}
		if len(gate.Checklist) > 0 {
			presenter.Notice(clioutput.RoleSuccess, "All checklist items confirmed.")
		}
	} else {
		// Preserve the original non-interactive protocol so tests, pipes, and
		// callers providing a single acknowledgement line remain compatible.
		for i, item := range gate.Checklist {
			presenter.TextLine("%d. %s", i+1, item.Text)
		}
	}

	presenter.Prompt("Type %q to confirm: ", gate.Acknowledgement.Value)
	line, err := readHumanLine(reader)
	if err != nil {
		return err
	}
	if gate.Acknowledgement.Type != "exact-text" {
		return fmt.Errorf("unsupported acknowledgement type %q", gate.Acknowledgement.Type)
	}
	if line != gate.Acknowledgement.Value {
		return fmt.Errorf("human gate %s not confirmed", id)
	}
	if gate.Evidence.Record != "" {
		evidence, err := e.recordName(gate.Evidence.Record, nil)
		if err != nil {
			return err
		}
		if evidence != "" {
			record = evidence
		}
	}
	if err := e.persistHumanEvidence(gate, record, head); err != nil {
		return err
	}
	if presenter.TTY {
		presenter.Notice(clioutput.RoleSuccess, "Human verification recorded.")
	}
	return nil
}

func (e *Engine) humanGateIsInteractive() bool {
	detect := e.HumanGateInteractive
	if detect == nil {
		detect = clioutput.IsInteractive
	}
	return detect(e.In, e.Out)
}

func readHumanLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (e *Engine) persistHumanEvidence(gate *workflow.HumanGate, record, head string) error {
	if err := e.Store.SetCommit(record, head); err != nil {
		return err
	}
	if gate.IdempotentRecord != "" {
		idempotent, err := e.recordName(gate.IdempotentRecord, nil)
		if err != nil {
			return err
		}
		if idempotent != "" && idempotent != record {
			return e.Store.SetCommit(idempotent, head)
		}
	}
	return nil
}

func (e *Engine) runCompletion(ctx context.Context, name string) (runErr error) {
	e.logEvent("completion_start", map[string]string{"completion": name})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("completion_end", map[string]string{"completion": name, "result": result})
	}()
	c, ok := e.Workflow.Spec.Completion[name]
	if !ok {
		return fmt.Errorf("unknown completion %q", name)
	}
	if err := e.assertMutationBoundary(false, e.lifecycleConfigured()); err != nil {
		return fmt.Errorf("completion %s failed its safety boundary: %w", name, err)
	}
	for _, a := range c.Assertions {
		if err := e.runAssertion(a); err != nil {
			return err
		}
	}
	if c.FinalValidation != "" {
		if err := e.runCompletionValidation(ctx, name, c.FinalValidation); err != nil {
			return err
		}
	}
	if c.Checkpoint.Uses != "" {
		if err := e.runTool(ctx, c.Checkpoint.Uses, nil); err != nil {
			return err
		}
	}
	for _, a := range c.AfterCheckpointAssertions {
		if err := e.runAssertion(a); err != nil {
			return err
		}
	}
	if err := e.assertMutationBoundary(true, e.lifecycleConfigured()); err != nil {
		return fmt.Errorf("completion %s failed its final safety boundary: %w", name, err)
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	marker := "complete"
	if c.WriteMarker.Record != "" {
		resolved, err := e.recordName(c.WriteMarker.Record, nil)
		if err != nil {
			return err
		}
		if resolved != "" {
			marker = resolved
		}
	}
	if c.FinalValidation != "" {
		if err := e.runInterruptionHook(interruptionBeforeCompletionMarker, PendingActorInvocation{
			Role:            "completion-marker",
			ValidationScope: completionValidationScope(name, c.FinalValidation),
		}); err != nil {
			return err
		}
	}
	if err := e.Store.SetCommit(marker, head); err != nil {
		return err
	}
	if marker != e.workflowCompleteMarker() {
		if err := e.Store.SetCommit(e.workflowCompleteMarker(), head); err != nil {
			return err
		}
	}
	if c.FinalValidation != "" {
		if err := e.runInterruptionHook(interruptionAfterCompletionMarker, PendingActorInvocation{
			Role:            "completion-marker",
			ValidationScope: completionValidationScope(name, c.FinalValidation),
		}); err != nil {
			return err
		}
	}
	if c.FinalValidation != "" {
		scope := completionValidationScope(name, c.FinalValidation)
		if err := e.clearStandaloneRepairStateForScope(scope); err != nil {
			return err
		}
	}
	base, _, _ := e.Store.Resolve(e.baseRecord())
	var branch string
	_, _ = e.Store.GetJSON(e.branchRecord(), &branch)
	changed, changedErr := e.changedImplementationFiles()
	log, _ := e.Repo.LogSince(base)
	presenter := e.presenter()
	presenter.Separator()
	presenter.CompletionSummary(e.Workflow.Metadata.Name)
	presenter.Metadata("Repository", presenter.Hyperlink(e.Repo.Root, clioutput.FileURL(e.Repo.Root)))
	presenter.Metadata("Branch", branch)
	presenter.Metadata("Base", e.commitLink(presenter, base))
	presenter.Metadata("Head", e.commitLink(presenter, head))
	if strings.TrimSpace(log) != "" {
		presenter.Notice(clioutput.RoleHeading, "Commits since base:")
		clioutput.NewPresenterWithPresentation(e.Out, clioutput.PresentationRaw).Raw(log)
	}
	if changedErr == nil {
		presenter.GitSummary("since base", changed)
	}
	if c.Summary.Title != "" {
		presenter.MetadataStyled("Summary", c.Summary.Title, clioutput.RoleSuccess)
	}
	for _, item := range c.Summary.Include {
		switch item {
		case "branch":
			presenter.Metadata("Branch", branch)
		case "base_commit":
			presenter.Metadata("Base", base)
		case "head_commit":
			presenter.Metadata("Head", head)
		case "state_directory":
			presenter.Metadata("State directory", e.Workflow.Spec.State.Directory)
		case "workspace_clean":
			dirty, err := e.implementationDirtyFiles()
			if err != nil {
				return err
			}
			value := fmt.Sprintf("%t", len(dirty) == 0)
			role := clioutput.RoleWarning
			if len(dirty) == 0 {
				role = clioutput.RoleSuccess
			}
			presenter.MetadataStyled("Workspace clean", value, role)
		case "canonical_gate_green":
			presenter.MetadataStyled("Canonical gate", "green", clioutput.RoleSuccess)
		case "commits_since_base", "changed_files_since_base":
			// The detailed values are already emitted above; keep this summary
			// vocabulary deterministic without inventing a second data model.
		}
	}
	return nil
}

// runCompletionValidation runs a final gate in its own durable evidence scope.
func (e *Engine) runCompletionValidation(ctx context.Context, completion, validation string) error {
	previous := e.completionValidation
	e.completionValidation = completion
	defer func() { e.completionValidation = previous }()
	if err := e.reconcileV1Alpha1CompletionValidationState(completion, validation); err != nil {
		return err
	}
	return e.runValidation(ctx, validation, nil)
}

// reconcileV1Alpha1CompletionValidationState recognizes the unscoped durable
// state written by the runtime before completion validations had their own
// namespace. It deliberately treats a same-named standalone validation as
// ambiguous and fails closed: a legacy consumed repair budget is also consumed
// by the completion validation. The legacy record is retained because this
// runtime cannot prove which old invocation created it.
//
// Safety evidence is recognized directly rather than copied so its bounded
// diagnostic output and actor/commit attribution remain exactly authoritative.
// Repair state is copied only when the new scope has no record. Writing the new
// record first makes an interruption safe and repeated entry idempotent; no
// migration path deletes the legacy record.
func (e *Engine) reconcileV1Alpha1CompletionValidationState(completion, validation string) error {
	if e.Workflow.APIVersion != "agentflow.dev/v1alpha1" {
		return nil
	}

	var legacyFailure validationFailureEvidence
	legacyFailureRecord := e.standaloneFailureRecordForScope(validation)
	ok, err := e.Store.GetJSON(legacyFailureRecord, &legacyFailure)
	if err != nil {
		return err
	}
	if ok && legacyFailure.FailureKind == PhaseFailureSafety {
		return safetyViolationFromEvidence(legacyFailure)
	}

	scope := completionValidationScope(completion, validation)
	newRepairRecord := e.standaloneRepairRecordForScope(scope)
	var current standaloneRepairState
	scopedExists, err := e.Store.GetJSON(newRepairRecord, &current)
	if err != nil {
		return err
	}
	if scopedExists && current.Attempts < 0 {
		return fmt.Errorf("invalid completion repair budget for validation %q", validation)
	}

	var legacy standaloneRepairState
	ok, err = e.Store.GetJSON(e.standaloneRepairRecordForScope(validation), &legacy)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if legacy.Attempts < 0 {
		return fmt.Errorf("invalid completion repair budget for validation %q", validation)
	}
	if legacy.Attempts <= current.Attempts {
		return nil
	}
	return e.Store.SetJSON(newRepairRecord, standaloneRepairState{Attempts: legacy.Attempts})
}

func (e *Engine) commitLink(presenter clioutput.Presenter, commit string) string {
	link, ok := e.Repo.CommitURL(commit)
	if !ok {
		return commit
	}
	return presenter.Hyperlink(commit, link)
}

func (e *Engine) runAssertion(a workflow.Assertion) error {
	if a.Uses != "" {
		switch a.Uses {
		case "assert-change-scope":
			return e.assertScope()
		case "assert-regex":
			p := a.With.Path
			r := a.With.Regex
			var err error
			p, err = e.context(nil).Expand(p)
			if err != nil {
				return err
			}
			return e.assertFileRegex(p, r)
		default:
			return fmt.Errorf("unsupported assertion tool %q", a.Uses)
		}
	}
	switch a.Type {
	case "progress-empty":
		n, err := e.uncheckedCount()
		if err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("progress contains %d unchecked items", n)
		}
		return nil
	case "workspace-integrity", "integrity-baseline-unchanged":
		return e.assertIntegrity()
	case "implementation-workspace-clean":
		d, err := e.implementationDirtyFiles()
		if err != nil {
			return err
		}
		if len(d) > 0 {
			return fmt.Errorf("implementation workspace dirty: %s", strings.Join(d, ", "))
		}
		return nil
	default:
		return fmt.Errorf("unsupported assertion type %q", a.Type)
	}
}

func (e *Engine) assertFileRegex(path, pattern string) error {
	b, err := os.ReadFile(filepath.Join(e.Repo.Root, path))
	if err != nil {
		return err
	}
	re, err := regexp.Compile("(?m)" + pattern)
	if err != nil {
		return err
	}
	if !re.Match(b) {
		return fmt.Errorf("%s does not match %s", path, pattern)
	}
	return nil
}
