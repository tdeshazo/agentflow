package observability

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func newLogRepo(t *testing.T) gitstate.Repo {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return gitstate.Repo{Root: dir}
}
