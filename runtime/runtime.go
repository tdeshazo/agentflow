// Package runtime exposes the supported embedding boundary for AgentFlow.
// Providers and deterministic tools are injected explicitly; no registration
// globals or init hooks are used.
package runtime

import (
	"context"
	"fmt"

	"github.com/tdeshazo/agentflow/internal/engine"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	"github.com/tdeshazo/agentflow/tool"
)

// Config describes one explicitly wired runtime instance.
type Config struct {
	WorkflowPath string
	RepoRoot     string
	Overrides    map[string]string
	Providers    map[string]provider.Provider
	ToolRegistry *tool.Registry
}

// Runtime is an importable facade over the workflow engine.
type Runtime struct{ engine *engine.Engine }

// New loads a workflow and preflights every injected extension before any
// durable run state or temporary workspace is created.
func New(config Config) (*Runtime, error) {
	if config.WorkflowPath == "" {
		return nil, fmt.Errorf("workflow path is required")
	}
	w, err := workflow.Load(config.WorkflowPath)
	if err != nil {
		return nil, err
	}
	e, err := engine.New(w, config.Providers, engine.Options{
		RepoRoot: config.RepoRoot, Overrides: config.Overrides, ToolRegistry: config.ToolRegistry,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{engine: e}, nil
}

// Run executes the loaded workflow.
func (r *Runtime) Run(ctx context.Context) error {
	if r == nil || r.engine == nil {
		return fmt.Errorf("runtime is nil")
	}
	return r.engine.Run(ctx)
}

// ExecuteTool executes one named configured tool through runtime enforcement.
func (r *Runtime) ExecuteTool(ctx context.Context, name string) error {
	if r == nil || r.engine == nil {
		return fmt.Errorf("runtime is nil")
	}
	return r.engine.ExecuteTool(ctx, name)
}
