//go:build !windows

package agentflowcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func consumeForegroundAckForTest(t *testing.T, cmd *exec.Cmd) <-chan startupAck {
	t.Helper()
	if len(cmd.ExtraFiles) != 2 {
		t.Fatalf("foreground readiness files = %d", len(cmd.ExtraFiles))
	}
	fd, err := syscall.Dup(int(cmd.ExtraFiles[1].Fd()))
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "test-foreground-ack")
	result := make(chan startupAck, 1)
	go func() {
		defer file.Close()
		var ack startupAck
		if json.NewDecoder(file).Decode(&ack) == nil {
			result <- ack
		}
		close(result)
	}()
	return result
}
