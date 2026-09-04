//go:build windows

package agentflowcli

import (
	"os/exec"
	"testing"
)

func consumeForegroundAckForTest(t *testing.T, cmd *exec.Cmd) <-chan startupAck {
	t.Helper()
	// Runtime transport coverage lives in readiness_windows_test.go. This helper
	// is not used by cross-build-only tests.
	result := make(chan startupAck)
	close(result)
	return result
}
