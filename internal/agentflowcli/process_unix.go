//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package agentflowcli

import (
	"os/exec"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
