// Package observability contains non-authoritative workflow discovery and
// runtime log helpers. It never writes acceptance records.
package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

// MaxLogBytes bounds one workflow's private operational log. Logs are
// diagnostic only, so reaching this limit must never alter workflow
// acceptance or execution authority.
const MaxLogBytes int64 = 1 << 20

// ErrLogCapacity reports that diagnostic storage is full.
var ErrLogCapacity = errors.New("workflow log capacity exceeded")

// LogStore is one workflow's append-only local runtime log. The file is
// outside the worktree and is never referenced by Git acceptance state.
type LogStore struct {
	Path string
	file *os.File
	mu   sync.Mutex
	size int64
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
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create workflow log directory: %w", err)
	}
	if err := validatePrivateDirectory(directory); err != nil {
		return nil, err
	}
	file, err := openPrivateAppend(path)
	if err != nil {
		return nil, fmt.Errorf("open workflow log: %w", err)
	}
	_ = file.Chmod(0o600)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat workflow log: %w", err)
	}
	if err := validatePrivateFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &LogStore{Path: path, file: file, size: info.Size()}, nil
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
	line := append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(line)) > MaxLogBytes {
		return ErrLogCapacity
	}
	count, err := l.file.Write(line)
	l.size += int64(count)
	return err
}

// Read returns the complete log, or os.ErrNotExist when no runtime log exists.
func Read(repo gitstate.Repo, workflowName string) ([]byte, string, error) {
	path, err := Path(repo, workflowName)
	if err != nil {
		return nil, "", err
	}
	file, err := openPrivateRead(path)
	if err != nil {
		return nil, path, err
	}
	defer file.Close()
	if err := validatePrivateLog(file); err != nil {
		return nil, path, err
	}
	b, err := io.ReadAll(file)
	return b, path, err
}

// ReadBounded reads the final limit bytes of a private runtime log and returns
// the exact byte cursor reached while reading for a lossless replay-to-follow
// handoff.
// A partial first JSONL record is discarded rather than decoded.
func ReadBounded(repo gitstate.Repo, workflowName string, limit int64) ([]byte, string, int64, error) {
	if limit <= 0 {
		return nil, "", 0, fmt.Errorf("workflow log read limit must be positive")
	}
	path, err := Path(repo, workflowName)
	if err != nil {
		return nil, "", 0, err
	}
	file, err := openPrivateRead(path)
	if err != nil {
		return nil, path, 0, err
	}
	defer file.Close()
	if err := validatePrivateLog(file); err != nil {
		return nil, path, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, path, 0, err
	}
	start := int64(0)
	if info.Size() > limit {
		start = info.Size() - limit
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, path, 0, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, path, 0, err
	}
	cursor, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, path, 0, err
	}
	if start != 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = []byte{}
		}
	}
	return data, path, cursor, nil
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
	file, err := openPrivateRead(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := validatePrivateLog(file); err != nil {
		return err
	}
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

// ReplayProcessOutput extracts at most limit captured process-output records
// in chronological order. Operational events are deliberately not rendered by
// attach, and malformed JSONL fails closed rather than being treated as
// terminal output.
func ReplayProcessOutput(data []byte, limit int) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("process-output replay limit must not be negative")
	}
	type event struct {
		Event  string            `json:"event"`
		Fields map[string]string `json:"fields"`
	}
	outputs := make([]string, 0)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var decoded event
		if err := json.Unmarshal(line, &decoded); err != nil {
			return nil, fmt.Errorf("decode workflow log for process-output replay: %w", err)
		}
		if decoded.Event != "process_output" || decoded.Fields == nil {
			continue
		}
		stream := decoded.Fields["stream"]
		if stream != "stdout" && stream != "stderr" {
			return nil, fmt.Errorf("workflow log has invalid process-output stream")
		}
		outputs = append(outputs, decoded.Fields["data"])
	}
	if limit == 0 || len(outputs) == 0 {
		return []byte{}, nil
	}
	if len(outputs) > limit {
		outputs = outputs[len(outputs)-limit:]
	}
	return []byte(strings.Join(outputs, "")), nil
}

// FollowProcessOutput writes future captured process output while ignoring
// non-output operational events. It uses the existing local log cursor and is
// cancellable without signaling the supervised workflow.
func FollowProcessOutput(ctx context.Context, path string, out io.Writer) error {
	return FollowProcessOutputFrom(ctx, path, -1, out)
}

// FollowProcessOutputFrom streams output appended after offset. An offset of
// -1 starts at the current end. Attach captures the log length before replay
// and passes it here, closing the replay-to-follow handoff window without
// replaying unbounded historical output.
func FollowProcessOutputFrom(ctx context.Context, path string, offset int64, out io.Writer) error {
	file, err := openPrivateRead(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := validatePrivateLog(file); err != nil {
		return err
	}
	if offset < 0 {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return err
		}
	} else if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(file)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for {
			line, readErr := reader.ReadBytes('\n')
			if len(line) > 0 {
				if err := writeProcessOutputLine(line, out); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func validatePrivateLog(file *os.File) error {
	return validatePrivateFile(file)
}

func writeProcessOutputLine(line []byte, out io.Writer) error {
	type event struct {
		Event  string            `json:"event"`
		Fields map[string]string `json:"fields"`
	}
	var decoded event
	if err := json.Unmarshal(bytes.TrimSpace(line), &decoded); err != nil {
		return fmt.Errorf("decode workflow log for process-output stream: %w", err)
	}
	if decoded.Event != "process_output" || decoded.Fields == nil {
		return nil
	}
	if stream := decoded.Fields["stream"]; stream != "stdout" && stream != "stderr" {
		return fmt.Errorf("workflow log has invalid process-output stream")
	}
	_, err := io.WriteString(out, decoded.Fields["data"])
	return err
}
