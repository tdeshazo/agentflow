//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package agentflowcli

import "os/exec"

func configureDetachedProcess(*exec.Cmd) {}
