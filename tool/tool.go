// Package tool defines the explicit registration contract for deterministic
// validation tools.
package tool

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// ContractVersionV1 is the first stable AgentFlow tool-plugin contract.
const ContractVersionV1 = "agentflow.dev/tool/v1"

// Mutation declares whether a tool is permitted to change the workspace.
type Mutation string

const (
	MutationNone      Mutation = "none"
	MutationWorkspace Mutation = "workspace"
)

// Descriptor identifies a plugin type and its declared effect boundary.
type Descriptor struct {
	Version             string   `json:"version" yaml:"version"`
	Type                string   `json:"type" yaml:"type"`
	Mutation            Mutation `json:"mutation" yaml:"mutation"`
	BehaviorFingerprint string   `json:"behaviorFingerprint,omitempty" yaml:"behaviorFingerprint,omitempty"`
}

// Invocation is the portable deterministic-tool execution boundary.
type Invocation struct {
	Name      string
	Workspace string
}

// Plugin is a versioned tool registration. DecodeConfig must reject malformed
// values before Run is eligible to mutate the workspace.
type Plugin interface {
	Descriptor() Descriptor
	DecodeConfig(map[string]any) (any, error)
	Run(context.Context, Invocation, any) error
}

// Registry contains explicitly registered plugins. It has no global state and
// is safe to construct and pass per Engine.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{plugins: map[string]Plugin{}}
}

func (r *Registry) Register(plugin Plugin) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	if plugin == nil {
		return fmt.Errorf("tool plugin is nil")
	}
	descriptor := plugin.Descriptor()
	if err := validateDescriptor(descriptor); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plugins == nil {
		r.plugins = map[string]Plugin{}
	}
	if _, ok := r.plugins[descriptor.Type]; ok {
		return fmt.Errorf("tool plugin type %q is already registered", descriptor.Type)
	}
	r.plugins[descriptor.Type] = registeredPlugin{descriptor: descriptor, plugin: plugin}
	return nil
}

type registeredPlugin struct {
	descriptor Descriptor
	plugin     Plugin
}

func (p registeredPlugin) Descriptor() Descriptor { return p.descriptor }
func (p registeredPlugin) DecodeConfig(raw map[string]any) (any, error) {
	return p.plugin.DecodeConfig(raw)
}
func (p registeredPlugin) Run(ctx context.Context, invocation Invocation, config any) error {
	return p.plugin.Run(ctx, invocation, config)
}

func (r *Registry) Lookup(kind string) (Plugin, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	plugin, ok := r.plugins[kind]
	return plugin, ok
}

// Types returns registered plugin types in deterministic order.
func (r *Registry) Types() []string {
	if r == nil {
		return []string{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.plugins))
	for kind := range r.plugins {
		types = append(types, kind)
	}
	sort.Strings(types)
	return types
}

func validateDescriptor(descriptor Descriptor) error {
	if descriptor.Version != ContractVersionV1 {
		return fmt.Errorf("unsupported tool plugin contract version %q", descriptor.Version)
	}
	if descriptor.Type == "" {
		return fmt.Errorf("tool plugin type is required")
	}
	if descriptor.Mutation != MutationNone && descriptor.Mutation != MutationWorkspace {
		return fmt.Errorf("tool plugin %q has unsupported mutation declaration %q", descriptor.Type, descriptor.Mutation)
	}
	if descriptor.BehaviorFingerprint != "" && !behaviorFingerprintPattern.MatchString(descriptor.BehaviorFingerprint) {
		return fmt.Errorf("tool plugin %q has invalid behavior fingerprint %q", descriptor.Type, descriptor.BehaviorFingerprint)
	}
	return nil
}

var behaviorFingerprintPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+/-]{0,127}$`)

// Cacheable reports whether the descriptor provides immutable behavior
// identity suitable for durable validation evidence.
func (d Descriptor) Cacheable() bool {
	return d.Mutation == MutationNone && d.BehaviorFingerprint != "" && behaviorFingerprintPattern.MatchString(d.BehaviorFingerprint)
}

type typedPlugin[C any] struct {
	descriptor Descriptor
	run        func(context.Context, Invocation, C) error
}

// NewTyped constructs a plugin whose configuration is decoded with strict YAML
// field checking and supplied to its runner as the declared Go type.
func NewTyped[C any](descriptor Descriptor, run func(context.Context, Invocation, C) error) Plugin {
	return typedPlugin[C]{descriptor: descriptor, run: run}
}

func (p typedPlugin[C]) Descriptor() Descriptor { return p.descriptor }

func (p typedPlugin[C]) DecodeConfig(raw map[string]any) (any, error) {
	encoded, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode tool config: %w", err)
	}
	var config C
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode typed tool config: %w", err)
	}
	return config, nil
}

func (p typedPlugin[C]) Run(ctx context.Context, invocation Invocation, config any) error {
	typed, ok := config.(C)
	if !ok {
		return fmt.Errorf("tool plugin %q received incompatible decoded configuration", p.descriptor.Type)
	}
	return p.run(ctx, invocation, typed)
}
