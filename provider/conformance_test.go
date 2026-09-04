package provider_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/provider"
	"github.com/tdeshazo/agentflow/provider/codex"
	"github.com/tdeshazo/agentflow/provider/local"
)

type adapterFactory struct {
	name string
	make func(*testing.T, string) provider.Provider
}

func TestProviderContractBehavioralConformance(t *testing.T) {
	factories := []adapterFactory{
		{name: "codex", make: func(t *testing.T, body string) provider.Provider {
			return codex.Provider{Binary: executable(t, body)}
		}},
		{name: "local", make: func(_ *testing.T, body string) provider.Provider {
			return local.Provider{Command: []string{"sh", "-c", body}}
		}},
	}
	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			workspace := t.TempDir()
			request := provider.Request{Workspace: workspace, Context: provider.InvocationContext{Version: provider.InvocationContextVersion}}
			success := factory.make(t, "cat >/dev/null; exit 0")
			if err := provider.VerifyContract(success); err != nil {
				t.Fatal(err)
			}
			if _, err := success.Run(context.Background(), request); err != nil {
				t.Fatalf("success: %v", err)
			}

			failure := factory.make(t, "cat >/dev/null; exit 7")
			if _, err := failure.Run(context.Background(), request); err == nil {
				t.Fatal("error path succeeded")
			}

			cancelled := factory.make(t, "cat >/dev/null; exec sleep 30")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()
			started := time.Now()
			if _, err := cancelled.Run(ctx, request); err == nil {
				t.Fatal("cancelled invocation succeeded")
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second {
				t.Fatalf("cancellation took %s", elapsed)
			}
		})
	}
}

func executable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
