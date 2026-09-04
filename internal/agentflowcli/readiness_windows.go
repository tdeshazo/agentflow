//go:build windows

package agentflowcli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const (
	readyWriteEnv = "AGENTFLOW_READY_WRITE_HANDLE"
	readyReadEnv  = "AGENTFLOW_READY_READ_HANDLE"
)

type readinessTransport struct {
	statusRead  *os.File
	statusWrite *os.File
	ackRead     *os.File
	ackWrite    *os.File
}

func newReadinessTransport() (*readinessTransport, error) {
	statusRead, statusWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	ackRead, ackWrite, err := os.Pipe()
	if err != nil {
		statusRead.Close()
		statusWrite.Close()
		return nil, err
	}
	return &readinessTransport{statusRead: statusRead, statusWrite: statusWrite, ackRead: ackRead, ackWrite: ackWrite}, nil
}

func (t *readinessTransport) configure(cmd *exec.Cmd, env []string) []string {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles,
		syscall.Handle(t.statusWrite.Fd()), syscall.Handle(t.ackRead.Fd()))
	return append(env,
		readyWriteEnv+"="+strconv.FormatUint(uint64(t.statusWrite.Fd()), 16),
		readyReadEnv+"="+strconv.FormatUint(uint64(t.ackRead.Fd()), 16))
}

func (t *readinessTransport) closeChildEnds() { _ = t.statusWrite.Close(); _ = t.ackRead.Close() }
func (t *readinessTransport) close() {
	_ = t.statusRead.Close()
	_ = t.statusWrite.Close()
	_ = t.ackRead.Close()
	_ = t.ackWrite.Close()
}

func openChildReadiness() (*os.File, *os.File, error) {
	writeHandle, err := strconv.ParseUint(os.Getenv(readyWriteEnv), 16, 64)
	if err != nil || writeHandle == 0 {
		return nil, nil, fmt.Errorf("invalid readiness write handle")
	}
	readHandle, err := strconv.ParseUint(os.Getenv(readyReadEnv), 16, 64)
	if err != nil || readHandle == 0 {
		return nil, nil, fmt.Errorf("invalid readiness read handle")
	}
	return os.NewFile(uintptr(writeHandle), "agentflow-ready-write"), os.NewFile(uintptr(readHandle), "agentflow-ready-read"), nil
}
