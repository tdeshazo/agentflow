// Package executiontrace persists versioned, non-authoritative orchestration
// traces separately from workflow definitions and acceptance state.
package executiontrace

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

// SchemaVersion is the current execution-trace event schema.
const SchemaVersion = 1

// Event is one observed orchestration transition. Fields must contain only
// bounded, non-secret metadata selected by the runtime.
type Event struct {
	SchemaVersion   int               `json:"schema_version"`
	Sequence        uint64            `json:"sequence"`
	Time            string            `json:"time"`
	RunID           string            `json:"run_id"`
	Event           string            `json:"event"`
	NodeID          string            `json:"node_id,omitempty"`
	NodeExecutionID string            `json:"node_execution_id,omitempty"`
	Attempt         int               `json:"attempt,omitempty"`
	Fields          map[string]string `json:"fields,omitempty"`
}

// Store appends events for one run. A Store may be shared by parallel node
// engines; writes and sequence allocation are serialized.
type Store struct {
	Path     string
	runID    string
	file     *os.File
	mu       sync.Mutex
	sequence uint64
}

// Path resolves the private, run-specific trace path under the repository's
// Git directory.
func Path(repo gitstate.Repo, runID string) (string, error) {
	if !validRunID(runID) {
		return "", fmt.Errorf("run id is invalid")
	}
	return repo.GitPath(filepath.Join("agentflow", "traces", runID+".jsonl"))
}

func validRunID(runID string) bool {
	if len(runID) != len("run_")+32 || runID[:len("run_")] != "run_" {
		return false
	}
	decoded, err := hex.DecodeString(runID[len("run_"):])
	return err == nil && len(decoded) == 16
}

// Open opens an existing trace or creates a new one, validating every existing
// event before allocating the next monotonic sequence number.
func Open(repo gitstate.Repo, runID string) (*Store, error) {
	path, err := Path(repo, runID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create execution trace directory: %w", err)
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	sequence, err := validateExisting(path, runID)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open execution trace: %w", err)
	}
	_ = file.Chmod(0o600)
	return &Store{Path: path, runID: runID, file: file, sequence: sequence}, nil
}

func validateExisting(path, runID string) (uint64, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open existing execution trace: %w", err)
	}
	defer file.Close()
	var sequence uint64
	var validBytes int64
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	scanner.Split(scanCompletedLines)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return 0, fmt.Errorf("decode execution trace event: %w", err)
		}
		if event.SchemaVersion != SchemaVersion {
			return 0, fmt.Errorf("unsupported execution trace schema version %d", event.SchemaVersion)
		}
		if event.RunID != runID || event.Sequence != sequence+1 || event.Event == "" || event.Time == "" {
			return 0, fmt.Errorf("execution trace contains an incompatible event at sequence %d", event.Sequence)
		}
		sequence = event.Sequence
		validBytes += int64(len(scanner.Bytes()))
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read execution trace: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat execution trace: %w", err)
	}
	if validBytes < info.Size() {
		if err := file.Truncate(validBytes); err != nil {
			return 0, fmt.Errorf("truncate torn execution trace event: %w", err)
		}
	}
	return sequence, nil
}

func scanCompletedLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF {
		return len(data), nil, nil
	}
	return 0, nil, nil
}

// Append writes one event and synchronizes it before returning so a completed
// transition is not acknowledged only in process memory.
func (s *Store) Append(event Event) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return errors.New("execution trace is closed")
	}
	s.sequence++
	event.SchemaVersion = SchemaVersion
	event.Sequence = s.sequence
	event.Time = time.Now().UTC().Format(time.RFC3339Nano)
	event.RunID = s.runID
	b, err := json.Marshal(event)
	if err != nil {
		s.sequence--
		return err
	}
	if _, err := s.file.Write(append(b, '\n')); err != nil {
		s.sequence--
		return err
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync execution trace: %w", err)
	}
	return nil
}

// Close closes the append handle.
func (s *Store) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.file.Close()
	s.file = nil
	return err
}
