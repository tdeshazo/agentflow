package tool

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type testConfig struct {
	Message string `yaml:"message"`
}

func TestRegistryConcurrentRegisterLookupAndTypes(t *testing.T) {
	registry := NewRegistry()
	const count = 64
	var wg sync.WaitGroup
	for i := range count {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			kind := fmt.Sprintf("test.concurrent.%02d", i)
			plugin := NewTyped(Descriptor{Version: ContractVersionV1, Type: kind, Mutation: MutationNone, BehaviorFingerprint: "v1"}, func(context.Context, Invocation, struct{}) error { return nil })
			if err := registry.Register(plugin); err != nil {
				t.Errorf("Register(%s): %v", kind, err)
			}
			registry.Lookup(kind)
			_ = registry.Types()
		}()
	}
	wg.Wait()
	if got := len(registry.Types()); got != count {
		t.Fatalf("Types() count = %d, want %d", got, count)
	}
}

func TestRegistryDuplicateAndFingerprintValidation(t *testing.T) {
	registry := NewRegistry()
	plugin := NewTyped(Descriptor{Version: ContractVersionV1, Type: "test.duplicate", Mutation: MutationNone}, func(context.Context, Invocation, struct{}) error { return nil })
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(plugin); err == nil || err.Error() != `tool plugin type "test.duplicate" is already registered` {
		t.Fatalf("duplicate Register() = %v", err)
	}
	if plugin.Descriptor().Cacheable() {
		t.Fatal("plugin without fingerprint is cacheable")
	}
	invalid := NewTyped(Descriptor{Version: ContractVersionV1, Type: "test.invalid", Mutation: MutationNone, BehaviorFingerprint: "bad fingerprint"}, func(context.Context, Invocation, struct{}) error { return nil })
	if err := registry.Register(invalid); err == nil {
		t.Fatal("Register() accepted invalid fingerprint")
	}
}

func TestRegistryDecodesTypedConfigurationStrictly(t *testing.T) {
	registry := NewRegistry()
	plugin := NewTyped(Descriptor{Version: ContractVersionV1, Type: "test.echo", Mutation: MutationNone}, func(_ context.Context, _ Invocation, config testConfig) error {
		if config.Message != "ready" {
			t.Fatalf("config = %#v", config)
		}
		return nil
	})
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeConfig(map[string]any{"message": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Run(context.Background(), Invocation{Name: "echo"}, decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.DecodeConfig(map[string]any{"unknown": true}); err == nil {
		t.Fatal("DecodeConfig() accepted unknown typed configuration field")
	}
}
