package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

func (e *Engine) checkpoint(label string, p *workflow.Phase) error {
	if err := e.assertScope(); err != nil {
		return err
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		if err := e.Repo.Add(dirty); err != nil {
			return err
		}
		staged, err := e.Repo.HasStagedChanges()
		if err != nil {
			return err
		}
		if staged {
			msg := e.Workflow.Spec.Workspace.Checkpointing.CommitMessage
			if msg == "" {
				msg = "AgentFlow: " + label
			}
			if p != nil {
				if expanded, err := e.context(p).Expand(msg); err == nil {
					msg = expanded
				}
			}
			if err := e.Repo.Commit(msg); err != nil {
				return err
			}
		}
	}
	dirty, err = e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("workspace remains dirty after checkpoint: %s", strings.Join(dirty, ", "))
	}
	return e.assertScope()
}
func (e *Engine) assertProgress(p *workflow.Phase, before int) error {
	checked, err := e.criterionChecked(p.Criterion)
	if err != nil {
		return err
	}
	if !checked {
		return fmt.Errorf("phase passed validation but criterion is not checked: %s", p.Criterion)
	}
	after, err := e.uncheckedCount()
	if err != nil {
		return err
	}
	delta := e.Workflow.Spec.Progress.Invariant.UncheckedCountDelta
	if delta == 0 {
		delta = -1
	}
	if after != before+delta {
		return fmt.Errorf("criterion progress mismatch: before=%d after=%d expected=%d", before, after, before+delta)
	}
	return nil
}
func (e *Engine) uncheckedCount() (int, error) {
	path, err := e.context(nil).Expand(e.Workflow.Spec.Progress.Source.Path)
	if err != nil {
		return 0, err
	}
	if path == "" {
		return 0, nil
	}
	b, err := os.ReadFile(filepath.Join(e.Repo.Root, path))
	if err != nil {
		return 0, err
	}
	re, err := regexp.Compile(e.Workflow.Spec.Progress.Source.UncheckedPattern)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if re.MatchString(line) {
			n++
		}
	}
	return n, nil
}
func (e *Engine) criterionChecked(criterion string) (bool, error) {
	path, err := e.context(nil).Expand(e.Workflow.Spec.Progress.Source.Path)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(filepath.Join(e.Repo.Root, path))
	if err != nil {
		return false, err
	}
	patterns := e.Workflow.Spec.Progress.Source.CheckedPatterns
	if e.Workflow.Spec.Progress.Source.CheckedPattern != "" {
		patterns = append(patterns, e.Workflow.Spec.Progress.Source.CheckedPattern)
	}
	for _, pat := range patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return false, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			m := re.FindStringSubmatch(line)
			if len(m) > 1 && m[1] == criterion {
				return true, nil
			}
		}
	}
	return false, nil
}
