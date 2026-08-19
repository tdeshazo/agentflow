package main

import (
	"fmt"
	"os"
	"os/exec"
)

// detachedStart is a variable so process-start failure and argument handling
// can be tested without creating a real child process.
var detachedStart = func(cmd *exec.Cmd) error { return cmd.Start() }

func launchDetachedRun(executable, workflowFile, repoRoot, codexBin string, setValues []string, workflowName string) (int, error) {
	args := detachedRunArgs(workflowFile, repoRoot, codexBin, setValues)
	return launchDetachedCommand(executable, args, []string{detachedChildEnv + "=1"}, workflowName)
}

func launchDetachedCommand(executable string, args, extraEnv []string, identity string) (int, error) {
	cmd := exec.Command(executable, args...)
	configureDetachedProcess(cmd)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return 0, fmt.Errorf("prepare detached process %q: %w", identity, err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.Env = append(os.Environ(), extraEnv...)
	if err := detachedStart(cmd); err != nil {
		return 0, fmt.Errorf("start detached process %q: %w", identity, err)
	}
	if cmd.Process == nil {
		return 0, fmt.Errorf("start detached process %q: process handle was not returned", identity)
	}
	pid := cmd.Process.Pid
	// The launcher must not retain a process handle or wait for the child. The
	// child has its own session and stdin is already independent of this process.
	_ = cmd.Process.Release()
	return pid, nil
}

func detachedRunArgs(workflowFile, repoRoot, codexBin string, setValues []string) []string {
	args := []string{"run", "-f", workflowFile}
	if repoRoot != "" {
		args = append(args, "-C", repoRoot)
	}
	if codexBin != "" {
		args = append(args, "--codex-bin", codexBin)
	}
	for _, value := range setValues {
		args = append(args, "--set", value)
	}
	return args
}
