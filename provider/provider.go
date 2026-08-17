// Package provider defines the interface for external workflow execution providers.
// Providers execute agent-owned units of work on behalf of the workflow engine.
package provider

import "context"

// Provider executes an AI-owned unit of work. The interpreter owns lifecycle,
// validation, checkpointing, and completion; providers only perform the requested work.
type Provider interface {
	Name() string
	Run(context.Context, Request) (Result, error)
}

// Request specifies work to be performed by a provider.
// It is intentionally provider-neutral. Provider-specific adapters translate
// these capabilities into their native CLI/API options.
type Request struct {
	Workspace string
	Model     string
	Reasoning string
	Prompt    string
	Sandbox   string
	Approval  string
	Ephemeral bool
	Color     string
	Metadata  map[string]string
}

// Result contains provider output useful for audit and debugging purposes.
// Workflow acceptance never depends solely on this result; deterministic validation
// decides advancement.
type Result struct {
	FinalMessage string
}
