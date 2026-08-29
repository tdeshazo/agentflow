package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

func (e *Engine) checkpoint(label string, p *workflow.Phase) (runErr error) {
	e.logEvent("checkpoint_start", map[string]string{"label": label})
	defer func() {
		result := "success"
		if runErr != nil {
			result = "failure"
		}
		e.logEvent("checkpoint_end", map[string]string{"label": label, "result": result})
	}()
	if err := e.assertMutationBoundary(false, e.runtimeOwnsPhaseLifecycle(p)); err != nil {
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
				msg = checkpointCommitLabel(label, p)
				if p != nil {
					if expanded, err := e.context(p).Expand(msg); err == nil {
						msg = expanded
					}
				}
				msg = checkpointCommitSubject(msg)
			} else if p != nil {
				if expanded, err := e.context(p).Expand(msg); err == nil {
					msg = expanded
				}
			}
			if err := e.Repo.CommitPaths(msg, dirty); err != nil {
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
	return e.assertMutationBoundary(true, e.runtimeOwnsPhaseLifecycle(p))
}

func checkpointCommitLabel(label string, p *workflow.Phase) string {
	if p == nil {
		return label
	}
	if p.Label != "" {
		return p.Label
	}
	if p.ID != "" {
		return p.ID
	}
	return label
}

func checkpointCommitSubject(label string) string {
	label = strings.NewReplacer("-", " ", "_", " ").Replace(label)
	label = strings.Join(strings.Fields(label), " ")
	if label == "" {
		return "Record workflow changes"
	}

	first, size := utf8.DecodeRuneInString(label)
	return string(unicode.ToUpper(first)) + label[size:]
}

func (e *Engine) assertProgress(p *workflow.Phase, active ActivePhase) error {
	criterion := p.Criterion
	if p.CriterionID != "" {
		criterion = p.CriterionID
	}
	return e.assertProgressAction(p, active, workflow.ProgressAssertion{Criterion: criterion})
}

func (e *Engine) assertProgressAction(p *workflow.Phase, active ActivePhase, assertion workflow.ProgressAssertion) error {
	criterion := assertion.Criterion
	if criterion == "" {
		criterion = p.CriterionID
		if criterion == "" {
			criterion = p.Criterion
		}
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
	var matches []progressItem
	for _, item := range snapshot.Items {
		if item.Text == criterion {
			matches = append(matches, item)
		}
	}
	if len(matches) > 1 {
		return false, fmt.Errorf("criterion %q matches multiple progress items", criterion)
	}
	if len(matches) == 1 {
		return matches[0].Checked, nil
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

func (p progressSnapshot) ItemStates() []ProgressItemState {
	out := make([]ProgressItemState, 0, len(p.Items))
	for _, item := range p.Items {
		out = append(out, ProgressItemState{Text: item.Text, Checked: item.Checked})
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

func (e *Engine) criterionIDForText(text string) (string, error) {
	var matches []string
	for _, criterion := range e.Workflow.Spec.Progress.Criteria {
		if criterion.Text == text {
			matches = append(matches, criterion.ID)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("next unchecked progress item %q does not resolve to exactly one declared criterion", text)
	}
	return matches[0], nil
}

// phaseCriterion resolves a phase target to its immutable criterion ID and
// display text. The legacy selector remains readable for v1alpha1 documents,
// but a text selector must resolve uniquely.
func (e *Engine) phaseCriterion(p *workflow.Phase) (string, string, error) {
	if p == nil {
		return "", "", fmt.Errorf("phase is required for a criterion target")
	}
	if p.CriterionID != "" {
		for _, criterion := range e.Workflow.Spec.Progress.Criteria {
			if criterion.ID == p.CriterionID {
				return criterion.ID, criterion.Text, nil
			}
		}
		return "", "", fmt.Errorf("phase %s references unknown criterion id %q", p.ID, p.CriterionID)
	}
	value, err := e.context(p).Expand(p.Criterion)
	if err != nil {
		return "", "", err
	}
	for _, criterion := range e.Workflow.Spec.Progress.Criteria {
		if criterion.ID == value {
			return criterion.ID, criterion.Text, nil
		}
	}
	var matches []workflow.Criterion
	for _, criterion := range e.Workflow.Spec.Progress.Criteria {
		if criterion.Text == value {
			matches = append(matches, criterion)
		}
	}
	if len(matches) != 1 {
		return "", "", fmt.Errorf("phase %s criterion %q does not resolve to exactly one declared criterion", p.ID, value)
	}
	return matches[0].ID, matches[0].Text, nil
}

func (e *Engine) declaredCriterionStates(snapshot progressSnapshot) (map[string]bool, error) {
	states := make(map[string]bool, len(e.Workflow.Spec.Progress.Criteria))
	for _, criterion := range e.Workflow.Spec.Progress.Criteria {
		var matches []progressItem
		for _, item := range snapshot.Items {
			if item.Text == criterion.Text {
				matches = append(matches, item)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("criterion %q must match exactly one progress item (matched %d)", criterion.ID, len(matches))
		}
		states[criterion.ID] = matches[0].Checked
	}
	return states, nil
}

func (e *Engine) assertProgressUnchanged(p *workflow.Phase, active ActivePhase) error {
	if active.TargetCriterionID == "" || len(active.CriteriaBefore) == 0 || len(active.ProgressItemsBefore) == 0 {
		return fmt.Errorf("phase %s is missing its durable engine-owned progress baseline", p.ID)
	}
	snapshot, err := e.progressSnapshot()
	if err != nil {
		return err
	}
	if !sameProgressItems(active.ProgressItemsBefore, snapshot.ItemStates()) {
		return fmt.Errorf("phase %s actor changed progress before deterministic acceptance", p.ID)
	}
	return nil
}

func (e *Engine) assertEngineProgressResult(p *workflow.Phase, active ActivePhase) error {
	_, text, err := e.phaseCriterion(p)
	if err != nil {
		return err
	}
	snapshot, err := e.progressSnapshot()
	if err != nil {
		return err
	}
	states, err := e.declaredCriterionStates(snapshot)
	if err != nil {
		return err
	}
	for id, before := range active.CriteriaBefore {
		want := before
		if id == active.TargetCriterionID {
			want = true
		}
		if states[id] != want {
			return fmt.Errorf("engine-owned progress transition for phase %s changed criterion %q outside its declared target", p.ID, id)
		}
	}
	if !states[active.TargetCriterionID] {
		return fmt.Errorf("engine-owned progress transition did not check target criterion %q (%s)", active.TargetCriterionID, text)
	}
	items := snapshot.ItemStates()
	if len(items) != len(active.ProgressItemsBefore) {
		return fmt.Errorf("engine-owned progress transition changed the checklist shape")
	}
	changes := 0
	for i, before := range active.ProgressItemsBefore {
		after := items[i]
		if before.Text != after.Text {
			return fmt.Errorf("engine-owned progress transition changed checklist content outside its target")
		}
		if before.Checked != after.Checked {
			if before.Text != text || before.Checked || !after.Checked {
				return fmt.Errorf("engine-owned progress transition changed checklist item %q outside its declared target", before.Text)
			}
			changes++
		}
	}
	if changes != 1 {
		return fmt.Errorf("engine-owned progress transition changed %d checklist items, want 1", changes)
	}
	delta := e.Workflow.Spec.Progress.Invariant.UncheckedCountDelta
	if delta == 0 {
		delta = -1
	}
	if snapshot.UncheckedCount != active.UncheckedBefore+delta {
		return fmt.Errorf("engine-owned progress mismatch: before=%d after=%d expected=%d", active.UncheckedBefore, snapshot.UncheckedCount, active.UncheckedBefore+delta)
	}
	return nil
}

func sameProgressItems(a, b []ProgressItemState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (e *Engine) advanceProgress(p *workflow.Phase, active *ActivePhase) error {
	if active.ProgressAdvanced {
		return e.assertEngineProgressResult(p, *active)
	}
	if err := e.assertProgressUnchanged(p, *active); err != nil {
		// A process can stop after the exact Markdown replacement and before it
		// persists ProgressAdvanced. Accept that only if the durable baseline
		// proves the current state is precisely the transition this engine owns.
		if resultErr := e.assertEngineProgressResult(p, *active); resultErr == nil {
			active.ProgressAdvanced = true
			active.ProgressAdvancePending = false
			return e.Store.SetJSON(e.activeRecord(), *active)
		}
		return err
	}
	_, text, err := e.phaseCriterion(p)
	if err != nil {
		return err
	}
	active.ProgressAdvancePending = true
	if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
		return err
	}
	if err := e.replaceMarkdownChecklist(p, e.Workflow.Spec.Progress.Source.Path, text, "checked"); err != nil {
		return err
	}
	if err := e.assertEngineProgressResult(p, *active); err != nil {
		return err
	}
	active.ProgressAdvancePending = false
	active.ProgressAdvanced = true
	return e.Store.SetJSON(e.activeRecord(), *active)
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
