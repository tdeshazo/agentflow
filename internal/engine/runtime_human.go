package engine

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

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
		fmt.Fprintf(e.Out, "==> Human gate %s already recorded\n", id)
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
			fmt.Fprintf(e.Out, "WARNING: %s\n", gate.Skip.Warning)
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
	fmt.Fprintf(e.Out, "\n=== Human verification: %s ===\n%s\n", id, gate.Instructions)
	for i, item := range gate.Checklist {
		fmt.Fprintf(e.Out, "%d. %s\n", i+1, item.Text)
	}
	fmt.Fprintf(e.Out, "Type %q to confirm: ", gate.Acknowledgement.Value)
	reader := bufio.NewReader(e.In)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	line = strings.TrimSpace(line)
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
	return e.persistHumanEvidence(gate, record, head)
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
		if err := e.runValidation(ctx, c.FinalValidation, nil); err != nil {
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
	if err := e.Store.SetCommit(marker, head); err != nil {
		return err
	}
	if marker != e.workflowCompleteMarker() {
		if err := e.Store.SetCommit(e.workflowCompleteMarker(), head); err != nil {
			return err
		}
	}
	base, _, _ := e.Store.Resolve(e.baseRecord())
	var branch string
	_, _ = e.Store.GetJSON(e.branchRecord(), &branch)
	changed, _ := e.changedImplementationFiles()
	log, _ := e.Repo.LogSince(base)
	fmt.Fprintf(e.Out, "\nWorkflow %s complete.\nBranch: %s\nBase: %s\nHead: %s\n", e.Workflow.Metadata.Name, branch, base, head)
	if strings.TrimSpace(log) != "" {
		fmt.Fprintf(e.Out, "Commits since base:\n%s", log)
	}
	if len(changed) > 0 {
		fmt.Fprintf(e.Out, "Changed files:\n- %s\n", strings.Join(changed, "\n- "))
	}
	if c.Summary.Title != "" {
		fmt.Fprintf(e.Out, "Summary: %s\n", c.Summary.Title)
	}
	for _, item := range c.Summary.Include {
		switch item {
		case "branch":
			fmt.Fprintf(e.Out, "Branch: %s\n", branch)
		case "base_commit":
			fmt.Fprintf(e.Out, "Base: %s\n", base)
		case "head_commit":
			fmt.Fprintf(e.Out, "Head: %s\n", head)
		case "state_directory":
			fmt.Fprintf(e.Out, "State directory: %s\n", e.Workflow.Spec.State.Directory)
		case "workspace_clean":
			dirty, err := e.implementationDirtyFiles()
			if err != nil {
				return err
			}
			fmt.Fprintf(e.Out, "Workspace clean: %t\n", len(dirty) == 0)
		case "canonical_gate_green":
			fmt.Fprintln(e.Out, "Canonical gate: green")
		case "commits_since_base", "changed_files_since_base":
			// The detailed values are already emitted above; keep this summary
			// vocabulary deterministic without inventing a second data model.
		}
	}
	return nil
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
