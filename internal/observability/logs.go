// Package observability contains non-authoritative workflow discovery and
// runtime log helpers. It never writes acceptance records.
package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

// LogStore is one workflow's append-only local runtime log. The file is
// outside the worktree and is never referenced by Git acceptance state.
type LogStore struct {
	Path string
	file *os.File
	mu   sync.Mutex
}

// Path resolves the deterministic, Git-aware log path for a workflow.
func Path(repo gitstate.Repo, workflowName string) (string, error) {
	if workflowName == "" {
		return "", fmt.Errorf("workflow name is required")
	}
	return repo.GitPath(filepath.Join("agentflow", "logs", filepath.Base(gitstate.NamespaceForWorkflow(workflowName))+".jsonl"))
}

// Open creates or opens a workflow log with restrictive permissions.
func Open(repo gitstate.Repo, workflowName string) (*LogStore, error) {
	path, err := Path(repo, workflowName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create workflow log directory: %w", err)
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open workflow log: %w", err)
	}
	_ = file.Chmod(0o600)
	return &LogStore{Path: path, file: file}, nil
}

// Close closes the append handle.
func (l *LogStore) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// Event appends one JSON Lines operational event. Fields are deliberately
// caller-selected so prompts, parameters, environments, and identity inputs
// are not persisted merely for observability.
func (l *LogStore) Event(kind string, fields map[string]string) error {
	if l == nil || l.file == nil {
		return nil
	}
	event := struct {
		Time   string            `json:"time"`
		Event  string            `json:"event"`
		Fields map[string]string `json:"fields,omitempty"`
	}{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: kind, Fields: fields}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.file.Write(append(b, '\n'))
	return err
}

// Read returns the complete log, or os.ErrNotExist when no runtime log exists.
func Read(repo gitstate.Repo, workflowName string) ([]byte, string, error) {
	path, err := Path(repo, workflowName)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(path)
	return b, path, err
}

// Tail returns the final n lines while preserving their original line endings.
func Tail(data []byte, n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("tail must not be negative")
	}
	if n == 0 || len(data) == 0 {
		return []byte{}, nil
	}
	starts := []int{0}
	for i, b := range data {
		if b == '\n' && i+1 < len(data) {
			starts = append(starts, i+1)
		}
	}
	if len(starts) > n {
		return data[starts[len(starts)-n]:], nil
	}
	return data, nil
}

// Follow writes existing and appended log content until ctx is cancelled.
// Cancellation belongs only to the logs reader; it never signals the workflow
// process recorded in the descriptor.
func Follow(ctx context.Context, path string, out io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := copyAvailable(reader, out); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := file.Seek(0, io.SeekCurrent); err != nil {
				return err
			}
		}
	}
}

func copyAvailable(reader *bufio.Reader, out io.Writer) error {
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := out.Write(line); writeErr != nil {
				return writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
