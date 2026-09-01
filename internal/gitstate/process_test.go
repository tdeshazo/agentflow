package gitstate

import "testing"

func TestProcessLivenessFailsClosedWhenInspectionUnavailable(t *testing.T) {
	metadata := &ProcessMetadata{PID: 123, Start: "456"}
	lookup := func(int) (string, processInspection) {
		return "", processInspectionUnavailable
	}

	if got, verified := processLiveness(metadata, lookup); verified || got != "" {
		t.Fatalf("unavailable process inspection liveness = %q, verified=%v", got, verified)
	}
}
