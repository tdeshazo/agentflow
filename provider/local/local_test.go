package local

import (
	"context"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/provider"
)

func TestProviderRejectsUnenforceableActorBoundary(t *testing.T) {
	p := Provider{Command: []string{"sh", "-c", "cat >/dev/null"}}
	_, err := p.Run(context.Background(), provider.Request{
		Context:            provider.InvocationContext{Version: provider.InvocationContextVersion},
		FilesystemBoundary: []provider.FilesystemRule{{Path: "/authoritative", Access: provider.FilesystemDeny}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot enforce") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestProviderRunsConfiguredLocalCommand(t *testing.T) {
	p := Provider{Command: []string{"sh", "-c", "printf local"}}
	result, err := p.Run(context.Background(), provider.Request{
		Workspace: t.TempDir(),
		Context:   provider.InvocationContext{Version: provider.InvocationContextVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage != "local" {
		t.Fatalf("FinalMessage = %q, want local", result.FinalMessage)
	}
}
