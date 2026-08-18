package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	_, err := launchDetachedRun("agentflow", "workflow.yaml", "/repo", "codex", nil, "example")
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
