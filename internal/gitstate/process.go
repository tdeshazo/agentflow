package gitstate

import (
	"os"
)

// CurrentProcessMetadata returns a PID and kernel process-start token when the
// host exposes Linux /proc metadata. Without the token, PID liveness is not
// safe to report.
func CurrentProcessMetadata() *ProcessMetadata {
	pid := os.Getpid()
	start, ok := processStartToken(pid)
	if !ok {
		return nil
	}
	return &ProcessMetadata{PID: pid, Start: start}
}

// ProcessLiveness verifies both the PID and its durable start token.
func ProcessLiveness(metadata *ProcessMetadata) (string, bool) {
	if metadata == nil || metadata.PID <= 0 || metadata.Start == "" {
		return "", false
	}
	start, ok := processStartToken(metadata.PID)
	if !ok {
		return "not_running", true
	}
	if start == metadata.Start {
		return "running", true
	}
	return "not_running", true
}
