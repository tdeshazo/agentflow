package agentflowcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tdeshazo/agentflow/internal/engine"
)

// detachedStart is a variable so process-start failure and argument handling
// can be tested without creating a real child process.
var detachedStart = func(cmd *exec.Cmd) error { return cmd.Start() }
var detachedReadyTimeout = 10 * time.Second

const foregroundChildEnv = "AGENTFLOW_FOREGROUND_CHILD"

type detachedStartup struct {
	OK         bool   `json:"ok"`
	RunID      string `json:"run_id,omitempty"`
	Attachable bool   `json:"attachable,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
}

type startupAck struct {
	Attached bool   `json:"attached"`
	Error    string `json:"error,omitempty"`
}

type launchedForeground struct {
	pid       int
	startup   detachedStartup
	transport *readinessTransport
}

func launchDetachedRun(executable, workflowFile, repoRoot, codexBin string, setValues []string, workflowName string) (int, bool, error) {
	args := detachedRunArgs(workflowFile, repoRoot, codexBin, setValues)
	launched, err := launchSupervisedCommandReady(executable, args, []string{detachedChildEnv + "=1"}, workflowName, false)
	if err != nil {
		return 0, false, err
	}
	return launched.pid, launched.startup.Attachable, nil
}

func launchForegroundRun(executable, workflowFile, repoRoot, codexBin string, setValues []string, workflowName string) (*launchedForeground, error) {
	args := detachedRunArgs(workflowFile, repoRoot, codexBin, setValues)
	return launchSupervisedCommandReady(executable, args, []string{detachedChildEnv + "=1", foregroundChildEnv + "=1"}, workflowName, true)
}

func launchDetachedCommandReady(executable string, args, extraEnv []string, identity string) (int, bool, error) {
	launched, err := launchSupervisedCommandReady(executable, args, extraEnv, identity, false)
	if err != nil {
		return 0, false, err
	}
	return launched.pid, launched.startup.Attachable, nil
}

func launchSupervisedCommandReady(executable string, args, extraEnv []string, identity string, waitForAttach bool) (*launchedForeground, error) {
	transport, err := newReadinessTransport()
	if err != nil {
		return nil, fmt.Errorf("prepare detached readiness for %q: %w", identity, err)
	}
	failed := true
	defer func() {
		if failed {
			transport.close()
		}
	}()
	cmd := exec.Command(executable, args...)
	configureDetachedProcess(cmd)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("prepare detached process %q: %w", identity, err)
	}
	defer devNull.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	environment := make([]string, 0, len(os.Environ())+len(extraEnv)+1)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, readyWriteEnv+"=") || strings.HasPrefix(value, readyReadEnv+"=") || strings.HasPrefix(value, detachedChildEnv+"=") || strings.HasPrefix(value, foregroundChildEnv+"=") {
			continue
		}
		environment = append(environment, value)
	}
	cmd.Env = transport.configure(cmd, append(environment, extraEnv...))
	if err := detachedStart(cmd); err != nil {
		return nil, fmt.Errorf("start detached process %q: %w", identity, err)
	}
	transport.closeChildEnds()
	if cmd.Process == nil {
		return nil, fmt.Errorf("start detached process %q: process handle was not returned", identity)
	}
	response := make(chan detachedStartup, 1)
	readErr := make(chan error, 1)
	go func() {
		var startup detachedStartup
		if err := json.NewDecoder(io.LimitReader(transport.statusRead, 8192)).Decode(&startup); err != nil {
			readErr <- err
			return
		}
		response <- startup
	}()
	select {
	case startup := <-response:
		if !startup.OK || startup.RunID == "" {
			stopDetachedProcess(cmd)
			return nil, fmt.Errorf("detached process %q failed before readiness: %s", identity, startup.Error)
		}
		launched := &launchedForeground{pid: cmd.Process.Pid, startup: startup, transport: transport}
		_ = cmd.Process.Release()
		failed = false
		_ = transport.statusRead.Close()
		if !waitForAttach {
			transport.close()
			launched.transport = nil
		}
		return launched, nil
	case err := <-readErr:
		stopDetachedProcess(cmd)
		return nil, fmt.Errorf("detached process %q exited before readiness: %w", identity, err)
	case <-time.After(detachedReadyTimeout):
		stopDetachedProcess(cmd)
		return nil, fmt.Errorf("detached process %q did not report readiness within %s", identity, detachedReadyTimeout)
	}
}

func (l *launchedForeground) acknowledge(attached bool, attachErr error) error {
	if l == nil || l.transport == nil {
		return nil
	}
	defer l.transport.close()
	ack := startupAck{Attached: attached}
	if attachErr != nil {
		ack.Error = boundedStartupError(attachErr)
	}
	return json.NewEncoder(l.transport.ackWrite).Encode(ack)
}

func stopDetachedProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func detachedSessionReady() (func(engine.SessionStatus) error, func(error)) {
	if os.Getenv(detachedChildEnv) != "1" {
		return nil, func(error) {}
	}
	statusWrite, ackRead, err := openChildReadiness()
	if err != nil {
		return nil, func(error) {}
	}
	_ = os.Unsetenv(readyWriteEnv)
	_ = os.Unsetenv(readyReadEnv)
	var once bool
	report := func(startup detachedStartup) error {
		if once {
			return nil
		}
		once = true
		encodeErr := json.NewEncoder(statusWrite).Encode(startup)
		closeErr := statusWrite.Close()
		if encodeErr != nil || os.Getenv(foregroundChildEnv) != "1" || !startup.OK || !startup.Attachable {
			_ = ackRead.Close()
			return errors.Join(encodeErr, closeErr)
		}
		result := make(chan error, 1)
		go func() {
			var ack startupAck
			if err := json.NewDecoder(io.LimitReader(ackRead, 4096)).Decode(&ack); err != nil {
				result <- err
				return
			}
			if !ack.Attached {
				result <- fmt.Errorf("foreground launcher did not attach: %s", ack.Error)
				return
			}
			result <- nil
		}()
		select {
		case err := <-result:
			_ = ackRead.Close()
			return errors.Join(encodeErr, closeErr, err)
		case <-time.After(detachedReadyTimeout):
			_ = ackRead.Close()
			return fmt.Errorf("foreground launcher did not acknowledge attachment within %s", detachedReadyTimeout)
		}
	}
	ready := func(status engine.SessionStatus) error {
		return report(detachedStartup{OK: true, RunID: status.RunID, Attachable: status.Attachable, Reason: status.Reason})
	}
	fail := func(runErr error) {
		if runErr != nil {
			_ = report(detachedStartup{Error: boundedStartupError(runErr)})
		}
	}
	return ready, fail
}

func boundedStartupError(err error) string {
	text := err.Error()
	if len(text) > 512 {
		text = text[:512]
	}
	return text
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
