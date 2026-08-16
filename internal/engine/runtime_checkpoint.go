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
func (e *Engine) assertProgress(p *workflow.Phase, active ActivePhase) error {
	return e.assertProgressAction(p, active, workflow.ProgressAssertion{Criterion: p.Criterion})
}

func (e *Engine) assertProgressAction(p *workflow.Phase, active ActivePhase, assertion workflow.ProgressAssertion) error {
	criterion := assertion.Criterion
	if criterion == "" {
		criterion = p.Criterion
	}
	criterion, err := e.context(p).Expand(criterion)
	if err != nil {
		return err
	}
	criterion, err = e.criterionText(criterion)
	if err != nil {
		return err
	}
	checked, err := e.criterionChecked(criterion)
	if err != nil {
		return err
	}
	if !checked {
		return fmt.Errorf("phase passed validation but criterion is not checked: %s", criterion)
	}
	progress, err := e.progressSnapshot()
	if err != nil {
		return err
	}
	before := active.UncheckedBefore
	if assertion.UncheckedCountBefore != "" {
		before, err = e.integer(p, assertion.UncheckedCountBefore)
		if err != nil {
			return err
		}
	}
	delta := assertion.UncheckedCountDelta
	if delta == 0 {
		delta = e.Workflow.Spec.Progress.Invariant.UncheckedCountDelta
	}
	if delta == 0 {
		delta = -1
	}
	if e.Workflow.Spec.Progress.Invariant.NoOtherMayClose {
		beforeChecked := map[string]bool{}
		for _, text := range active.CheckedBefore {
			beforeChecked[text] = true
		}
		for _, text := range progress.CheckedTexts() {
			if text != criterion && !beforeChecked[text] {
				return fmt.Errorf("criterion progress violation: unrelated criterion was checked: %s", text)
			}
		}
	}
	if progress.UncheckedCount != before+delta {
		return fmt.Errorf("criterion progress mismatch: before=%d after=%d expected=%d", before, progress.UncheckedCount, before+delta)
	}
	return nil
}
func (e *Engine) uncheckedCount() (int, error) {
	snapshot, err := e.progressSnapshot()
	return snapshot.UncheckedCount, err
}
func (e *Engine) criterionChecked(criterion string) (bool, error) {
	snapshot, err := e.progressSnapshot()
	if err != nil {
		return false, err
	}
	for _, item := range snapshot.Items {
		if item.Text == criterion {
			return item.Checked, nil
		}
	}
	return false, nil
}

type progressItem struct {
	Text    string
	Checked bool
}
type progressSnapshot struct {
	Items          []progressItem
	UncheckedCount int
}

func (p progressSnapshot) CheckedTexts() []string {
	var out []string
	for _, item := range p.Items {
		if item.Checked {
			out = append(out, item.Text)
		}
	}
	return out
}
func (p progressSnapshot) NextUnchecked() string {
	for _, item := range p.Items {
		if !item.Checked {
			return item.Text
		}
	}
	return ""
}

func (e *Engine) progressSnapshot() (progressSnapshot, error) {
	source := e.Workflow.Spec.Progress.Source
	if source.Path == "" {
		return progressSnapshot{}, nil
	}
	path, err := e.contextWithoutProgress(nil).Expand(source.Path)
	if err != nil {
		return progressSnapshot{}, err
	}
	b, err := os.ReadFile(filepath.Join(e.Repo.Root, path))
	if err != nil {
		return progressSnapshot{}, err
	}
	unchecked, err := regexp.Compile(source.UncheckedPattern)
	if err != nil {
		return progressSnapshot{}, err
	}
	patterns := append([]string{}, source.CheckedPatterns...)
	if source.CheckedPattern != "" {
		patterns = append(patterns, source.CheckedPattern)
	}
	checked := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return progressSnapshot{}, err
		}
		checked = append(checked, re)
	}
	snapshot := progressSnapshot{}
	for _, line := range strings.Split(string(b), "\n") {
		if m := unchecked.FindStringSubmatch(line); len(m) > 1 {
			snapshot.Items = append(snapshot.Items, progressItem{Text: m[1]})
			snapshot.UncheckedCount++
			continue
		}
		for _, re := range checked {
			if m := re.FindStringSubmatch(line); len(m) > 1 {
				snapshot.Items = append(snapshot.Items, progressItem{Text: m[1], Checked: true})
				break
			}
		}
	}
	return snapshot, nil
}

func (e *Engine) criterionText(value string) (string, error) {
	for _, criterion := range e.Workflow.Spec.Progress.Criteria {
		if criterion.ID == value {
			return criterion.Text, nil
		}
	}
	return value, nil
}

func (e *Engine) progressContext() (workflow.ProgressContext, error) {
	snapshot, err := e.progressSnapshot()
	if err != nil {
		return workflow.ProgressContext{}, err
	}
	return workflow.ProgressContext{UncheckedCount: snapshot.UncheckedCount, NextUnchecked: snapshot.NextUnchecked(), IsChecked: func(criterion string) (bool, error) {
		text, err := e.criterionText(criterion)
		if err != nil {
			return false, err
		}
		for _, item := range snapshot.Items {
			if item.Text == text {
				return item.Checked, nil
			}
		}
		return false, nil
	}}, nil
}
