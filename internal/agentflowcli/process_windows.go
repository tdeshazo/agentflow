//go:build windows

package agentflowcli

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow,
		HideWindow:    true,
	}
}
