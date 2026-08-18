//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package main

import "os/exec"

func configureDetachedProcess(*exec.Cmd) {}
