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

func (e *Engine) runFlowAssertion(a map[string]any) error {
	typeName := fmt.Sprint(a["type"])
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
func (e *Engine) runHuman(id string) error {
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
	if ok, _, err := e.validCommitMarker("human/" + id); err != nil {
		return err
	} else if ok {
		fmt.Fprintf(e.Out, "==> Human gate %s already recorded\n", id)
		return nil
	}
	required, err := e.context(nil).Bool(gate.When)
	if err != nil {
		return err
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if !required {
		if gate.Skip.Warning != "" {
			fmt.Fprintf(e.Out, "WARNING: %s\n", gate.Skip.Warning)
		}
		return e.Store.SetCommit("human/"+id, head)
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
	return e.Store.SetCommit("human/"+id, head)
}
func (e *Engine) runCompletion(ctx context.Context, name string) error {
	c, ok := e.Workflow.Spec.Completion[name]
	if !ok {
		return fmt.Errorf("unknown completion %q", name)
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
		if err := e.checkpoint(c.Checkpoint.Label, nil); err != nil {
			return err
		}
	}
	for _, a := range c.AfterCheckpointAssertions {
		if err := e.runAssertion(a); err != nil {
			return err
		}
	}
	head, err := e.Repo.Head()
	if err != nil {
		return err
	}
	if err := e.Store.SetCommit("complete", head); err != nil {
		return err
	}
	base, _, _ := e.Store.Resolve("base")
	var branch string
	_, _ = e.Store.GetJSON("branch", &branch)
	changed, _ := e.changedImplementationFiles()
	log, _ := e.Repo.LogSince(base)
	fmt.Fprintf(e.Out, "\nWorkflow %s complete.\nBranch: %s\nBase: %s\nHead: %s\n", e.Workflow.Metadata.Name, branch, base, head)
	if strings.TrimSpace(log) != "" {
		fmt.Fprintf(e.Out, "Commits since base:\n%s", log)
	}
	if len(changed) > 0 {
		fmt.Fprintf(e.Out, "Changed files:\n- %s\n", strings.Join(changed, "\n- "))
	}
	return nil
}
func (e *Engine) runAssertion(a workflow.Assertion) error {
	if a.Uses != "" {
		switch a.Uses {
		case "assert-change-scope":
			return e.assertScope()
		case "assert-regex":
			p := fmt.Sprint(a.With["path"])
			r := fmt.Sprint(a.With["regex"])
			var err error
			p, err = e.context(nil).Expand(p)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(filepath.Join(e.Repo.Root, p))
			if err != nil {
				return err
			}
			re, err := regexp.Compile(r)
			if err != nil {
				return err
			}
			if !re.Match(b) {
				return fmt.Errorf("%s does not match %s", p, r)
			}
			return nil
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
	case "workspace-integrity":
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
