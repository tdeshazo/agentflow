package agentflowcli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeDetachedTestReady(t *testing.T, cmd *exec.Cmd, attachable bool) {
	t.Helper()
	if len(cmd.ExtraFiles) < 1 {
		t.Fatalf("detached readiness files = %d", len(cmd.ExtraFiles))
	}
	if err := json.NewEncoder(cmd.ExtraFiles[0]).Encode(detachedStartup{OK: true, RunID: "run_0123456789abcdef0123456789abcdef", Attachable: attachable}); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedRunArgsPreserveInputsWithoutRecursiveDetach(t *testing.T) {
	args := detachedRunArgs("workflow.yaml", "/repo", "/bin/codex", []string{"task=one", "task=two"})
	want := []string{"run", "-f", "workflow.yaml", "-C", "/repo", "--codex-bin", "/bin/codex", "--set", "task=one", "--set", "task=two"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("detached args = %#v, want %#v", args, want)
	}
	for _, arg := range args {
		if arg == "--detach" {
			t.Fatal("detached child retained --detach")
		}
	}
}

func TestDetachedStartFailurePropagates(t *testing.T) {
	original := detachedStart
	t.Cleanup(func() { detachedStart = original })
	detachedStart = func(*exec.Cmd) error { return errors.New("permission denied") }

	_, _, err := launchDetachedRun("agentflow", "workflow.yaml", "/repo", "codex", nil, "example")
	if err == nil || !strings.Contains(err.Error(), `start detached process "example"`) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("detached start error = %v", err)
	}
}

func TestDetachedHelperOutlivesLauncherBoundaryAndDoesNotReadLauncherStdin(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "finished")
	script := `read ignored || true; sleep 0.2; printf done > "$1"`
	if _, err := launchDetachedCommand("/bin/sh", []string{"-c", script, "agentflow-test", marker}, nil, "helper"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "done" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("detached helper did not outlive launcher boundary; marker %q", marker)
}

func TestDetachedReadinessFailureAndTimeoutAreReported(t *testing.T) {
	t.Run("child failure", func(t *testing.T) {
		original := detachedStart
		t.Cleanup(func() { detachedStart = original })
		detachedStart = func(cmd *exec.Cmd) error {
			cmd.Process = &os.Process{Pid: 12345}
			if err := json.NewEncoder(cmd.ExtraFiles[0]).Encode(detachedStartup{Error: "invalid startup"}); err != nil {
				t.Fatal(err)
			}
			return nil
		}
		_, _, err := launchDetachedRun("agentflow", "workflow.yaml", "/repo", "codex", nil, "failed")
		if err == nil || !strings.Contains(err.Error(), "invalid startup") {
			t.Fatalf("startup failure = %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		original := detachedReadyTimeout
		t.Cleanup(func() { detachedReadyTimeout = original })
		detachedReadyTimeout = 50 * time.Millisecond
		_, _, err := launchDetachedCommandReady("/bin/sh", []string{"-c", "sleep 5"}, nil, "silent")
		if err == nil || !strings.Contains(err.Error(), "did not report readiness") {
			t.Fatalf("readiness timeout = %v", err)
		}
	})
}
