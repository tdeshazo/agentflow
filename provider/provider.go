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

// PresentationIntent describes the caller's desired human-facing presentation
// without naming a provider's command-line flags. Providers resolve it against
// their actual output destination.
type PresentationIntent string

const (
	// PresentationAutomatic lets the provider choose its native terminal
	// policy.
	PresentationAutomatic PresentationIntent = "automatic"
	// PresentationRich requests human-facing presentation when the destination
	// supports it. Providers translate this semantic request into their own
	// native option vocabulary.
	PresentationRich PresentationIntent = "rich"
	// PresentationPlain keeps provider output suitable for captured or
	// machine-facing streams.
	PresentationPlain PresentationIntent = "plain"

	// These aliases preserve source compatibility with the earlier intent API.
	PresentationAuto   = PresentationAutomatic
	PresentationAlways = PresentationRich
	PresentationNever  = PresentationPlain
)

// ResolvePresentationIntent converts a workflow-facing presentation value into
// a known provider intent. An omitted or unknown value uses the safe native
// default; each provider still resolves that intent against its output sink.
func ResolvePresentationIntent(value string) PresentationIntent {
	switch value {
	case "always":
		return PresentationRich
	case "never":
		return PresentationPlain
	case "auto", string(PresentationAutomatic):
		return PresentationAutomatic
	default:
		return PresentationAutomatic
	}
}

// Request specifies work to be performed by a provider.
// It is intentionally provider-neutral. Provider-specific adapters translate
// these capabilities into their native CLI/API options.
type Request struct {
	Workspace    string
	Model        string
	Reasoning    string
	Prompt       string
	Sandbox      string
	Approval     string
	Ephemeral    bool
	Presentation PresentationIntent
	Metadata     map[string]string
}

// Result contains provider output useful for audit and debugging purposes.
// Workflow acceptance never depends solely on this result; deterministic validation
// decides advancement.
type Result struct {
	FinalMessage string
}
