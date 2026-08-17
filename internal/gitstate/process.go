package gitstate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

func processStartToken(pid int) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	closeParen := strings.LastIndexByte(string(b), ')')
	if closeParen < 0 || closeParen+2 >= len(b) {
		return "", false
	}
	fields := strings.Fields(string(b)[closeParen+2:])
	// The slice starts at stat field 3 (state); field 22 (starttime) is index 19.
	if len(fields) <= 19 {
		return "", false
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false
	}
	return fields[19], true
}
