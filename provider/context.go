package provider

const (
	// InvocationContextVersionV1 remains the source-compatible context used by
	// providers that have not negotiated the fresh-context extension.
	InvocationContextVersionV1 = "agentflow.dev/invocation-context/v1"
	// InvocationContextVersionV2 adds a deterministic compilation receipt.
	InvocationContextVersionV2 = "agentflow.dev/invocation-context/v2"
	// InvocationContextVersion is retained for source compatibility.
	InvocationContextVersion = InvocationContextVersionV1
)

// WorkspacePlaceholder is the stable workspace identity emitted by the
// engine. Provider adapters replace it with their isolated workspace only
// while rendering a request.
const WorkspacePlaceholder = "{{ agentflow.workspace }}"

// InvocationContext is a provider-neutral, non-authoritative view compiled
// immediately before one actor invocation. It is never durable acceptance
// state and deliberately contains references to artifact content, not bodies.
type InvocationContext struct {
	Version      string                  `json:"version"`
	Invocation   InvocationIdentity      `json:"invocation"`
	Objective    string                  `json:"objective"`
	Workspace    WorkspaceContext        `json:"workspace"`
	Dependencies []DependencyContext     `json:"dependencies"`
	Artifacts    []ArtifactReference     `json:"artifacts"`
	Evidence     []EvidenceReference     `json:"evidence"`
	Handoffs     []HandoffReference      `json:"handoffs,omitempty"`
	Authority    InvocationAuthority     `json:"authority"`
	Executor     ExecutorCapabilities    `json:"executor"`
	Validations  []ValidationRequirement `json:"validations"`
	Failure      *RepairFailureEvidence  `json:"failure,omitempty"`
	Manifest     ContextManifest         `json:"manifest"`
	// Receipt is present only for v2. It permits a provider or operator to
	// account for selected context without receiving omitted payloads.
	Receipt *ContextReceipt `json:"receipt,omitempty"`
}

// ContextReceipt is the bounded, non-secret result of v2 compilation.
type ContextReceipt struct {
	CompilerVersion string            `json:"compilerVersion"`
	Digest          string            `json:"digest"`
	Bytes           int               `json:"bytes"`
	Selected        []string          `json:"selected"`
	Omitted         []ContextOmission `json:"omitted"`
}

type ContextOmission struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// InvocationIdentity identifies the actor unit without copying workflow
// history or provider output into the request.
type InvocationIdentity struct {
	Role       string `json:"role"`
	Actor      string `json:"actor"`
	Phase      string `json:"phase,omitempty"`
	PhaseKind  string `json:"phaseKind,omitempty"`
	Criterion  string `json:"criterion,omitempty"`
	Validation string `json:"validation,omitempty"`
}

// WorkspaceContext describes only invocation-relevant workspace state.
type WorkspaceContext struct {
	Root         string   `json:"root"`
	Head         string   `json:"head"`
	ChangedPaths []string `json:"changedPaths"`
	DirtyPaths   []string `json:"dirtyPaths"`
}

// DependencyContext identifies one accepted direct dependency.
type DependencyContext struct {
	Phase  string `json:"phase"`
	Commit string `json:"commit"`
}

// ArtifactReference points at verified workspace content. Content is read by
// the actor from WorkspacePlaceholder; it is never copied into the context.
type ArtifactReference struct {
	Name     string `json:"name"`
	Producer string `json:"producer"`
	Type     string `json:"type"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Mode     uint32 `json:"mode"`
}

// EvidenceReference identifies deterministic evidence accepted from a direct
// dependency.
type EvidenceReference struct {
	Name       string `json:"name"`
	Producer   string `json:"producer"`
	Validation string `json:"validation"`
}

// HandoffReference is an accepted advisory handoff from a direct dependency.
// It is bound to that dependency's accepted commit by the engine.
type HandoffReference struct {
	Producer string  `json:"producer"`
	Commit   string  `json:"commit"`
	Digest   string  `json:"digest"`
	Payload  Handoff `json:"payload"`
}

// InvocationAuthority is the effective runtime-enforced authority for one
// actor invocation.
type InvocationAuthority struct {
	WritablePaths []string        `json:"writablePaths"`
	Protected     []ProtectedPath `json:"protectedPaths"`
	ReadOnly      bool            `json:"readOnly"`
	RuntimeOwned  []string        `json:"runtimeOwnedPaths"`
	MayCommit     bool            `json:"mayCommit"`
	Resources     ResourceAccess  `json:"resources"`
}

// ProtectedPath preserves one authored integrity boundary and its exclusions.
type ProtectedPath struct {
	Rule     string   `json:"rule"`
	Path     string   `json:"path"`
	Excludes []string `json:"excludes"`
	Mode     string   `json:"mode"`
}

// ResourceAccess exposes conservative Stage 6 metadata. It describes the
// enforced filesystem boundary; it does not enforce token or other budgets.
type ResourceAccess struct {
	WorkspaceRead  string   `json:"workspaceRead"`
	WorkspaceWrite []string `json:"workspaceWrite"`
	ExcludedPaths  []string `json:"excludedPaths"`
}

// ExecutorCapabilities records effective capabilities relevant to actor work.
type ExecutorCapabilities struct {
	Sandbox            string          `json:"sandbox"`
	Approval           string          `json:"approval"`
	Ephemeral          bool            `json:"ephemeral"`
	FilesystemBoundary bool            `json:"filesystemBoundary"`
	Network            string          `json:"network"`
	Capabilities       []string        `json:"capabilities,omitempty"`
	Credentials        []string        `json:"credentials,omitempty"`
	ApprovalGate       string          `json:"approvalGate,omitempty"`
	Budgets            ResourceBudgets `json:"budgets"`
}

// ResourceBudgets exposes limits, never usage values or secret material.
type ResourceBudgets struct {
	ModelCalls int     `json:"modelCalls,omitempty"`
	ToolCalls  int     `json:"toolCalls,omitempty"`
	Tokens     int64   `json:"tokens,omitempty"`
	Duration   string  `json:"duration,omitempty"`
	CostUSD    float64 `json:"costUSD,omitempty"`
}

// ValidationRequirement names one deterministic validation selected for this
// invocation. Definitions remain workflow authority and are not copied here.
type ValidationRequirement struct {
	Name string `json:"name"`
}

// RepairFailureEvidence is the bounded, redacted failure selected for one
// repair invocation.
type RepairFailureEvidence struct {
	Validation string `json:"validation"`
	Kind       string `json:"kind"`
	Output     string `json:"output"`
}

// ContextManifest explains every included component and intentional omission.
type ContextManifest struct {
	Included []ContextManifestEntry `json:"included"`
	Excluded []ContextManifestEntry `json:"excluded"`
}

// ContextManifestEntry makes context selection inspectable without exposing
// the selected values in planning diagnostics.
type ContextManifestEntry struct {
	Component       string `json:"component"`
	Source          string `json:"source"`
	Reason          string `json:"reason"`
	RuntimeResolved bool   `json:"runtimeResolved,omitempty"`
}
