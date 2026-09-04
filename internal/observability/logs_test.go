package observability

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

func TestWorkflowLogsUseGitDirectoryAndRemainIsolated(t *testing.T) {
	repo := newLogRepo(t)
	first, err := Open(repo, "first/workflow")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(repo, "second/workflow")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	rel, err := filepath.Rel(repo.Root, first.Path)
	if first.Path == second.Path || !strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		t.Fatalf("logs are not isolated outside worktree: %q %q", first.Path, second.Path)
	}
	if err := first.Event("phase_start", map[string]string{"phase": "one"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Event("phase_start", map[string]string{"phase": "two"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(first.Path); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("log permissions = %v, err = %v", info.Mode().Perm(), err)
	}
	firstData, _, err := Read(repo, "first/workflow")
	if err != nil || !strings.Contains(string(firstData), `"phase":"one"`) || strings.Contains(string(firstData), "two") {
		t.Fatalf("first log = %q, err = %v", firstData, err)
	}
}

func TestTailRejectsNegativeAndReturnsFinalLines(t *testing.T) {
	data := []byte("one\ntwo\nthree\n")
	got, err := Tail(data, 2)
	if err != nil || string(got) != "two\nthree\n" {
		t.Fatalf("tail = %q, err = %v", got, err)
	}
	if _, err := Tail(data, -1); err == nil {
		t.Fatal("negative tail was accepted")
	}
	got, err = Tail(data, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("zero tail = %q, err = %v", got, err)
	}
}

func TestFollowCancellationDoesNotSignalWorkflowProcess(t *testing.T) {
	repo := newLogRepo(t)
	logStore, err := Open(repo, "follow")
	if err != nil {
		t.Fatal(err)
	}
	defer logStore.Close()
	if err := logStore.Event("workflow_start", nil); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("sh", "-c", "sleep 5")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer process.Process.Kill()

	ctx, cancel := context.WithCancel(context.Background())
	output := &bytes.Buffer{}
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, logStore.Path, output) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow did not cancel")
	}
	if err := process.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("follow cancellation affected workflow process: %v", err)
	}
}

func TestReplayProcessOutputIsBoundedAndIgnoresOperationalEvents(t *testing.T) {
	data := []byte("{\"event\":\"phase_start\"}\n" +
		"{\"event\":\"process_output\",\"fields\":{\"stream\":\"stdout\",\"data\":\"one\\n\"}}\n" +
		"{\"event\":\"process_output\",\"fields\":{\"stream\":\"stderr\",\"data\":\"two\\n\"}}\n")
	got, err := ReplayProcessOutput(data, 1)
	if err != nil || string(got) != "two\n" {
		t.Fatalf("replay = %q, err = %v", got, err)
	}
	if _, err := ReplayProcessOutput([]byte("not-json\n"), 1); err == nil {
		t.Fatal("malformed log was accepted for attach replay")
	}
}

func TestLogStorageAndAttachReadAreBounded(t *testing.T) {
	repo := newLogRepo(t)
	store, err := Open(repo, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Event("process_output", map[string]string{"stream": "stdout", "data": strings.Repeat("x", int(MaxLogBytes))}); !errors.Is(err, ErrLogCapacity) {
		t.Fatalf("log capacity error = %v", err)
	}
	if err := os.WriteFile(store.Path, []byte(strings.Repeat("x", 512)+"\n{\"event\":\"process_output\",\"fields\":{\"stream\":\"stdout\",\"data\":\"tail\"}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _, offset, err := ReadBounded(repo, "bounded", 100)
	if err != nil || offset <= int64(len(data)) || strings.Contains(string(data), strings.Repeat("x", 10)) || !strings.Contains(string(data), "tail") {
		t.Fatalf("bounded log read = %q offset=%d err=%v", data, offset, err)
	}
}

func TestFollowProcessOutputStreamsOnlyCapturedOutput(t *testing.T) {
	repo := newLogRepo(t)
	store, err := Open(repo, "attach-follow")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output synchronizedBuffer
	done := make(chan error, 1)
	go func() { done <- FollowProcessOutput(ctx, store.Path, &output) }()
	time.Sleep(150 * time.Millisecond)
	if err := store.Event("phase_start", map[string]string{"phase": "ignored"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Event("process_output", map[string]string{"stream": "stdout", "data": "attached\n"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && output.String() != "attached\n" {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if output.String() != "attached\n" {
		t.Fatalf("attached output = %q", output.String())
	}
}

func TestOpenRejectsSymlinkedPrivateLog(t *testing.T) {
	repo := newLogRepo(t)
	path, err := Path(repo, "symlink")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repo, "symlink"); err == nil {
		t.Fatal("symlinked private log was accepted")
	}
}

func newLogRepo(t *testing.T) gitstate.Repo {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return gitstate.Repo{Root: dir}
}

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
