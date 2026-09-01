package gitstate

import (
	"os"
)

type processInspection uint8

const (
	processInspectionUnavailable processInspection = iota
	processInspectionExited
	processInspectionIdentified
)

type processStartLookup func(int) (string, processInspection)

// CurrentProcessMetadata returns a PID and kernel process-start token when the
// host exposes Linux /proc metadata. Without the token, PID liveness is not
// safe to report.
func CurrentProcessMetadata() *ProcessMetadata {
	pid := os.Getpid()
	start, inspection := processStartToken(pid)
	if inspection != processInspectionIdentified {
		return nil
	}
	return &ProcessMetadata{PID: pid, Start: start}
}

// ProcessLiveness verifies both the PID and its durable start token.
func ProcessLiveness(metadata *ProcessMetadata) (string, bool) {
	return processLiveness(metadata, processStartToken)
}

func processLiveness(metadata *ProcessMetadata, lookup processStartLookup) (string, bool) {
	if metadata == nil || metadata.PID <= 0 || metadata.Start == "" {
		return "", false
	}
	start, inspection := lookup(metadata.PID)
	switch inspection {
	case processInspectionExited:
		return "not_running", true
	case processInspectionIdentified:
		if start == metadata.Start {
			return "running", true
		}
		return "not_running", true
	default:
		return "", false
	}
}
