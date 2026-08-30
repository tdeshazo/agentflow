package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const workItemRecordVersion = 1

// WorkItemState is the durable authority for one v1alpha4 work item. The
// optional Markdown adapter mirrors this state but never replaces it.
type WorkItemState struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
}

func (e *Engine) workItemRecord(id string) string {
	return "criteria/" + id
}

func (e *Engine) workItemState(id string) (WorkItemState, bool, error) {
	var state WorkItemState
	ok, err := e.Store.GetJSON(e.workItemRecord(id), &state)
	if err != nil || !ok {
		return state, ok, err
	}
	if state.Version != workItemRecordVersion || state.ID != id || state.Status != "completed" || state.Phase == "" {
		return WorkItemState{}, false, fmt.Errorf("work item %q has incompatible durable state", id)
	}
	return state, true, nil
}

func (e *Engine) assertWorkItemPending(phase *workflow.Phase) error {
	if phase == nil || !phase.AdvanceWorkItem {
		return nil
	}
	if state, ok, err := e.workItemState(phase.WorkItemID); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("phase %s target work item %q is already completed by phase %s", phase.ID, phase.WorkItemID, state.Phase)
	}
	return nil
}

func (e *Engine) assertWorkItemAccepted(phase *workflow.Phase) error {
	if phase == nil || !phase.AdvanceWorkItem {
		return nil
	}
	state, ok, err := e.workItemState(phase.WorkItemID)
	if err != nil {
		return err
	}
	if !ok || state.Phase != phase.ID {
		return fmt.Errorf("accepted phase %s is missing durable completion for work item %q", phase.ID, phase.WorkItemID)
	}
	return e.assertWorkItemAdapterMatchesState()
}

func (e *Engine) requireAllWorkItems() error {
	if len(e.Workflow.Spec.Criteria.Items) == 0 {
		return nil
	}
	for _, item := range e.Workflow.Spec.Criteria.Items {
		if _, ok, err := e.workItemState(item.ID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("workflow completion requires work item %q", item.ID)
		}
	}
	return e.assertWorkItemAdapterMatchesState()
}

func (e *Engine) advanceWorkItem(phase *workflow.Phase, active *ActivePhase) error {
	if phase == nil || active == nil || !phase.AdvanceWorkItem {
		return nil
	}
	if active.TargetWorkItemID != phase.WorkItemID {
		return fmt.Errorf("phase %s work item advancement has no matching durable target", phase.ID)
	}
	if active.WorkItemAdvanced {
		return e.assertWorkItemAccepted(phase)
	}
	if !active.WorkItemAdvancePending {
		if err := e.assertWorkItemPending(phase); err != nil {
			return err
		}
		current, err := e.workItemAdapterDigest()
		if err != nil {
			return err
		}
		if current != active.WorkItemAdapterBefore {
			return fmt.Errorf("phase %s actor changed the engine-owned Markdown work-item adapter before acceptance", phase.ID)
		}
		active.WorkItemAdvancePending = true
		if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
			return err
		}
		if err := e.Store.SetJSON(e.workItemRecord(phase.WorkItemID), WorkItemState{
			Version: workItemRecordVersion, ID: phase.WorkItemID, Phase: phase.ID, Status: "completed",
		}); err != nil {
			return fmt.Errorf("persist work item %q completion: %w", phase.WorkItemID, err)
		}
	}
	if err := e.syncWorkItemMarkdownAdapter(phase, active); err != nil {
		return err
	}
	active.WorkItemAdvancePending = false
	active.WorkItemAdvanced = true
	if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
		return err
	}
	return e.assertWorkItemAccepted(phase)
}

func (e *Engine) workItemAdapterDigest() (string, error) {
	adapter := e.Workflow.Spec.Criteria.MarkdownAdapter
	if adapter == nil {
		return "", nil
	}
	abs, err := e.markdownWorkspacePath(adapter.Path)
	if err != nil {
		return "", err
	}
	contents, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return digestBytes(contents), nil
}

func (e *Engine) syncWorkItemMarkdownAdapter(phase *workflow.Phase, active *ActivePhase) error {
	adapter := e.Workflow.Spec.Criteria.MarkdownAdapter
	if adapter == nil {
		return nil
	}
	label := adapter.Items[phase.WorkItemID]
	if label == "" {
		return fmt.Errorf("work item %q has no Markdown adapter label", phase.WorkItemID)
	}
	current, err := e.workItemAdapterDigest()
	if err != nil {
		return err
	}
	if active.WorkItemAdapterAfter != "" {
		if current != active.WorkItemAdapterAfter {
			return fmt.Errorf("Markdown work-item adapter changed outside the declared transition")
		}
		return nil
	}
	if current != active.WorkItemAdapterBefore {
		if err := e.assertWorkItemAdapterMatchesState(); err != nil {
			return err
		}
	} else if _, err := e.transitionMarkdownChecklist(adapter.Path, label, "checked"); err != nil {
		return err
	}
	after, err := e.workItemAdapterDigest()
	if err != nil {
		return err
	}
	active.WorkItemAdapterAfter = after
	return e.assertWorkItemAdapterMatchesState()
}

func (e *Engine) assertWorkItemAdapterMatchesState() error {
	adapter := e.Workflow.Spec.Criteria.MarkdownAdapter
	if adapter == nil {
		return nil
	}
	abs, err := e.markdownWorkspacePath(adapter.Path)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	for _, item := range e.Workflow.Spec.Criteria.Items {
		state, completed, err := e.workItemState(item.ID)
		if err != nil {
			return err
		}
		if completed && state.ID != item.ID {
			return fmt.Errorf("work item %q adapter state is incompatible", item.ID)
		}
		checked, err := markdownChecklistItemChecked(contents, adapter.Items[item.ID])
		if err != nil {
			return fmt.Errorf("Markdown work-item adapter: %w", err)
		}
		if checked != completed {
			return fmt.Errorf("Markdown work-item adapter does not match durable state for %q", item.ID)
		}
	}
	return nil
}

func markdownChecklistItemChecked(contents []byte, item string) (bool, error) {
	matches := 0
	checked := false
	for _, line := range splitMarkdownLines(contents) {
		parts := markdownChecklistLine.FindStringSubmatch(line)
		if len(parts) == 0 || parts[4] != item {
			continue
		}
		matches++
		checked = parts[2] == "x" || parts[2] == "X"
	}
	if matches != 1 {
		return false, fmt.Errorf("target %q matched %d items", item, matches)
	}
	return checked, nil
}

func splitMarkdownLines(contents []byte) []string {
	return strings.Split(string(contents), "\n")
}
