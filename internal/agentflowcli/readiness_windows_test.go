//go:build windows

package agentflowcli

import (
	"os/exec"
	"strings"
	"testing"
)

func TestWindowsReadinessUsesInheritedHandlesNotExtraFiles(t *testing.T) {
	transport, err := newReadinessTransport()
	if err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	cmd := exec.Command("agentflow.exe")
	configureDetachedProcess(cmd)
	env := transport.configure(cmd, nil)
	if len(cmd.ExtraFiles) != 0 {
		t.Fatalf("Windows readiness unexpectedly used ExtraFiles: %d", len(cmd.ExtraFiles))
	}
	if cmd.SysProcAttr == nil || len(cmd.SysProcAttr.AdditionalInheritedHandles) != 2 {
		t.Fatalf("inherited readiness handles = %#v", cmd.SysProcAttr)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, readyWriteEnv+"=") || !strings.Contains(joined, readyReadEnv+"=") {
		t.Fatalf("readiness handle environment = %q", joined)
	}
}
