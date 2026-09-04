//go:build !windows

package agentflowcli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

const (
	readyWriteEnv = "AGENTFLOW_READY_WRITE"
	readyReadEnv  = "AGENTFLOW_READY_READ"
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
	cmd.ExtraFiles = append(cmd.ExtraFiles, t.statusWrite, t.ackRead)
	return append(env, readyWriteEnv+"=3", readyReadEnv+"=4")
}

func (t *readinessTransport) closeChildEnds() { _ = t.statusWrite.Close(); _ = t.ackRead.Close() }
func (t *readinessTransport) close() {
	_ = t.statusRead.Close()
	_ = t.statusWrite.Close()
	_ = t.ackRead.Close()
	_ = t.ackWrite.Close()
}

func openChildReadiness() (*os.File, *os.File, error) {
	writeFD, err := strconv.Atoi(os.Getenv(readyWriteEnv))
	if err != nil || writeFD < 3 {
		return nil, nil, fmt.Errorf("invalid readiness write descriptor")
	}
	readFD, err := strconv.Atoi(os.Getenv(readyReadEnv))
	if err != nil || readFD < 3 {
		return nil, nil, fmt.Errorf("invalid readiness read descriptor")
	}
	return os.NewFile(uintptr(writeFD), "agentflow-ready-write"), os.NewFile(uintptr(readFD), "agentflow-ready-read"), nil
}
