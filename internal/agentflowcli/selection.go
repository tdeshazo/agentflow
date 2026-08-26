package agentflowcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

const activeSelectionPath = "agentflow/selection.json"

// workflowSelection is local CLI convenience state. It deliberately contains
// selector names only: it is not workflow execution state and cannot provide
// evidence for a phase, checkpoint, or completion decision.
type workflowSelection struct {
	SchemaVersion int    `json:"schema_version"`
	Current       string `json:"current"`
	Previous      string `json:"previous,omitempty"`
}

const workflowSelectionSchema = 1

// selectionStore keeps the current and previous logical workflow selectors in
// Git's worktree-specific metadata. GitPath is essential here: a linked
// worktree has a .git file, not a directory of its own.
type selectionStore struct {
	repo gitstate.Repo
}

func newSelectionStore(repo gitstate.Repo) selectionStore {
	return selectionStore{repo: repo}
}

func (s selectionStore) path() (string, error) {
	return s.repo.GitPath(activeSelectionPath)
}

// Read returns false when no selection has been made. Invalid persisted data
// is an error rather than an implicit empty selection so stale local metadata
// cannot silently change which workflow a command targets.
func (s selectionStore) Read() (workflowSelection, bool, error) {
	path, err := s.path()
	if err != nil {
		return workflowSelection{}, false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return workflowSelection{}, false, nil
	}
	if err != nil {
		return workflowSelection{}, false, fmt.Errorf("read active workflow selection: %w", err)
	}

	var selection workflowSelection
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return workflowSelection{}, false, fmt.Errorf("decode active workflow selection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return workflowSelection{}, false, fmt.Errorf("decode active workflow selection: expected one JSON object")
	}
	if err := selection.validate(); err != nil {
		return workflowSelection{}, false, fmt.Errorf("invalid active workflow selection: %w", err)
	}
	return selection, true, nil
}

func (s workflowSelection) validate() error {
	if s.SchemaVersion != workflowSelectionSchema {
		return fmt.Errorf("unsupported schema version %d", s.SchemaVersion)
	}
	if err := workflow.ValidateSelector(s.Current); err != nil {
		return fmt.Errorf("current: %w", err)
	}
	if s.Previous != "" {
		if err := workflow.ValidateSelector(s.Previous); err != nil {
			return fmt.Errorf("previous: %w", err)
		}
	}
	return nil
}

// Select replaces the current selector and retains the old current selector
// for switch -. Selecting the already-current workflow is a no-op, matching
// the useful branch-switching behavior of retaining the existing previous
// selection.
func (s selectionStore) Select(selector string) error {
	if err := workflow.ValidateSelector(selector); err != nil {
		return err
	}
	current, found, err := s.Read()
	if err != nil {
		return err
	}
	if found && current.Current == selector {
		return nil
	}
	next := workflowSelection{SchemaVersion: workflowSelectionSchema, Current: selector}
	if found {
		next.Previous = current.Current
	}
	return s.write(next)
}

// SwitchPrevious swaps the current and previous selectors, like git switch -.
func (s selectionStore) SwitchPrevious() (string, error) {
	selection, found, err := s.Read()
	if err != nil {
		return "", err
	}
	if !found || selection.Previous == "" {
		return "", fmt.Errorf("no previous workflow selection")
	}
	next := workflowSelection{
		SchemaVersion: workflowSelectionSchema,
		Current:       selection.Previous,
		Previous:      selection.Current,
	}
	if err := s.write(next); err != nil {
		return "", err
	}
	return next.Current, nil
}

// Clear removes local selection metadata only. It cannot alter execution
// state, workflow definitions, refs, or the implementation workspace.
func (s selectionStore) Clear() error {
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear active workflow selection: %w", err)
	}
	return nil
}

func (s selectionStore) write(selection workflowSelection) error {
	if err := selection.validate(); err != nil {
		return err
	}
	path, err := s.path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create active workflow selection directory: %w", err)
	}
	b, err := json.Marshal(selection)
	if err != nil {
		return fmt.Errorf("encode active workflow selection: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".selection-*")
	if err != nil {
		return fmt.Errorf("create active workflow selection: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set active workflow selection permissions: %w", err)
	}
	if _, err := temporary.Write(b); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write active workflow selection: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close active workflow selection: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace active workflow selection: %w", err)
	}
	return nil
}
