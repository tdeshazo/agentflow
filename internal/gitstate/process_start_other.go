//go:build !linux

package gitstate

func processStartToken(int) (string, bool) {
	// Without a portable process-start identity, reporting a PID as live would
	// risk confusing a reused PID with the detached workflow.
	return "", false
}
