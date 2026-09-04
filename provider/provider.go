// Package provider defines the interface for external workflow execution providers.
// Providers execute agent-owned units of work on behalf of the workflow engine.
package provider

import "context"

// Provider executes an AI-owned unit of work. The interpreter owns lifecycle,
// validation, checkpointing, and completion; providers only perform the
// requested work. Run may be called concurrently for independent scheduler
// nodes, so implementations must not mutate shared state without synchronization.
type Provider interface {
	Name() string
	Run(context.Context, Request) (Result, error)
}

// FilesystemBoundaryEnforcer is required for actor execution. It makes
// filesystem isolation an explicit adapter capability instead of an optional
// Request field that a provider could silently ignore.
type FilesystemBoundaryEnforcer interface {
	EnforcesFilesystemBoundary() bool
}

// ExecutionPolicyEnforcer is required for actor execution. A provider must
// translate the engine-owned network, capability, credential, and metering
// contract into an enforced process boundary or reject the request.
type ExecutionPolicyEnforcer interface {
	EnforcesExecutionPolicy() bool
}

// PresentationIntent describes the runtime's desired human-facing presentation
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

// ResolvePresentationIntent is retained for source compatibility at the
// engine/provider boundary. Presentation policy is runtime-owned: workflow
// agent data cannot override it. Foreground execution therefore uses the
// provider's automatic/native policy; the engine selects PresentationPlain at
// explicit captured boundaries such as detached execution.
func ResolvePresentationIntent(_ string) PresentationIntent {
	return PresentationAutomatic
}

// Request specifies work to be performed by a provider.
// It is intentionally provider-neutral. Provider-specific adapters translate
// these capabilities into their native CLI/API options.
type Request struct {
	Workspace string
	Model     string
	Reasoning string
	Context   InvocationContext
	Sandbox   string
	Approval  string
	Ephemeral bool
	// OutputLastMessage requests capture of the provider's final message for
	// diagnostic and presentation use. It never affects workflow acceptance.
	OutputLastMessage bool
	Presentation      PresentationIntent
	Metadata          map[string]string
	// FilesystemBoundary is an engine-owned read boundary for the provider's
	// actor process. Adapters must enforce every rule or reject the request.
	// It is not advisory prompt content.
	FilesystemBoundary []FilesystemRule
	Policy             ExecutionPolicy
	// Credentials contains only explicitly authorized values. Keys are target
	// environment names; values must never be logged or rendered into Context.
	Credentials map[string]string
	Budget      InvocationBudget
	// Handoff requests native structured output when a phase needs an advisory
	// direct-dependency handoff.
	Handoff *HandoffRequest
}

// ExecutionPolicy describes provider-enforced effects for one invocation.
type ExecutionPolicy struct {
	Network      string
	Capabilities []string
	ApprovalGate string
}

// InvocationBudget gives the adapter hard remaining limits. Zero is unbounded.
type InvocationBudget struct {
	Tokens   int64
	Duration int64 // nanoseconds
	CostUSD  float64
}

// FilesystemAccess is the access granted to one absolute filesystem path.
type FilesystemAccess string

const (
	FilesystemRead FilesystemAccess = "read"
	FilesystemDeny FilesystemAccess = "deny"
)

// FilesystemRule describes one provider process filesystem rule. Paths are
// canonical absolute paths and more-specific rules may narrow a parent rule.
type FilesystemRule struct {
	Path   string
	Access FilesystemAccess
}

// Result contains provider output useful for audit and debugging purposes.
// Workflow acceptance never depends solely on this result; deterministic validation
// decides advancement.
type Result struct {
	FinalMessage string
	Usage        Usage
	Handoff      []byte
}

// Usage is provider-reported metering for one invocation.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
}
