package engine

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// The semantic context types are the compiler's internal value model. They
// deliberately mirror the information that can be projected to an adapter,
// while remaining independent of a particular provider contract or wire type.

const (
	semanticContextEncoding       = "agentflow.dev/semantic-context/v1.0"
	semanticHandoffEncoding       = "agentflow.dev/semantic/v1"
	semanticWorkspacePlaceholder  = "{{ agentflow.workspace }}"
	semanticMaxHandoffBytes       = 12 * 1024
	semanticMaxHandoffStringBytes = 2 * 1024
	semanticMaxHandoffCollection  = 50
)

var (
	semanticHandoffSecret              = regexp.MustCompile(`(?i)(api[_-]?key|(?:access[_-]?)?token|secret|password|private[_-]?key)\s*[:=]`)
	semanticHandoffCredentialSignature = regexp.MustCompile(`(?i)(?:\bbearer\s+[a-z0-9._~+/=-]+|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|\b(?:gh[pousr]_|github_pat_|xox[baprs]-|sk-(?:proj-)?|AKIA)[a-z0-9_-]+)`)
	semanticWindowsVolumePrefix        = regexp.MustCompile(`^[A-Za-z]:`)
)

type semanticInvocationContext struct {
	Encoding     string                          `json:"version"`
	Invocation   semanticInvocationIdentity      `json:"invocation"`
	Objective    string                          `json:"objective"`
	Workspace    semanticWorkspaceContext        `json:"workspace"`
	Dependencies []semanticDependencyContext     `json:"dependencies"`
	Artifacts    []semanticArtifactReference     `json:"artifacts"`
	Evidence     []semanticEvidenceReference     `json:"evidence"`
	Handoffs     []semanticHandoffReference      `json:"handoffs,omitempty"`
	Authority    semanticInvocationAuthority     `json:"authority"`
	Executor     semanticExecutorCapabilities    `json:"executor"`
	Validations  []semanticValidationRequirement `json:"validations"`
	Failure      *semanticRepairFailureEvidence  `json:"failure,omitempty"`
	Manifest     semanticContextManifest         `json:"manifest"`
	Receipt      *semanticContextReceipt         `json:"receipt,omitempty"`
	Fresh        bool                            `json:"-"`
}

type semanticContextReceipt struct {
	CompilerVersion string                    `json:"compilerVersion"`
	Digest          string                    `json:"digest"`
	Bytes           int                       `json:"bytes"`
	Selected        []string                  `json:"selected"`
	Omitted         []semanticContextOmission `json:"omitted"`
}

type semanticContextOmission struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}
type semanticInvocationIdentity struct {
	Role       string `json:"role"`
	Actor      string `json:"actor"`
	Phase      string `json:"phase,omitempty"`
	PhaseKind  string `json:"phaseKind,omitempty"`
	Criterion  string `json:"criterion,omitempty"`
	Validation string `json:"validation,omitempty"`
}
type semanticWorkspaceContext struct {
	Root         string   `json:"root"`
	Head         string   `json:"head"`
	ChangedPaths []string `json:"changedPaths"`
	DirtyPaths   []string `json:"dirtyPaths"`
}
type semanticDependencyContext struct {
	Phase  string `json:"phase"`
	Commit string `json:"commit"`
}
type semanticArtifactReference struct {
	Name     string `json:"name"`
	Producer string `json:"producer"`
	Type     string `json:"type"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Mode     uint32 `json:"mode"`
}
type semanticEvidenceReference struct {
	Name       string `json:"name"`
	Producer   string `json:"producer"`
	Validation string `json:"validation"`
}
type semanticHandoffReference struct {
	Producer string          `json:"producer"`
	Commit   string          `json:"commit"`
	Digest   string          `json:"digest"`
	Payload  semanticHandoff `json:"payload"`
}
type semanticInvocationAuthority struct {
	WritablePaths []string                `json:"writablePaths"`
	Protected     []semanticProtectedPath `json:"protectedPaths"`
	ReadOnly      bool                    `json:"readOnly"`
	RuntimeOwned  []string                `json:"runtimeOwnedPaths"`
	MayCommit     bool                    `json:"mayCommit"`
	Resources     semanticResourceAccess  `json:"resources"`
}
type semanticProtectedPath struct {
	Rule     string   `json:"rule"`
	Path     string   `json:"path"`
	Excludes []string `json:"excludes"`
	Mode     string   `json:"mode"`
}
type semanticResourceAccess struct {
	WorkspaceRead  string   `json:"workspaceRead"`
	WorkspaceWrite []string `json:"workspaceWrite"`
	ExcludedPaths  []string `json:"excludedPaths"`
}
type semanticExecutorCapabilities struct {
	Sandbox            string                  `json:"sandbox"`
	Approval           string                  `json:"approval"`
	Ephemeral          bool                    `json:"ephemeral"`
	FilesystemBoundary bool                    `json:"filesystemBoundary"`
	Network            string                  `json:"network"`
	Capabilities       []string                `json:"capabilities,omitempty"`
	Credentials        []string                `json:"credentials,omitempty"`
	ApprovalGate       string                  `json:"approvalGate,omitempty"`
	Budgets            semanticResourceBudgets `json:"budgets"`
}
type semanticResourceBudgets struct {
	ModelCalls int     `json:"modelCalls,omitempty"`
	ToolCalls  int     `json:"toolCalls,omitempty"`
	Tokens     int64   `json:"tokens,omitempty"`
	Duration   string  `json:"duration,omitempty"`
	CostUSD    float64 `json:"costUSD,omitempty"`
}
type semanticValidationRequirement struct {
	Name string `json:"name"`
}
type semanticRepairFailureEvidence struct {
	Validation string `json:"validation"`
	Kind       string `json:"kind"`
	Output     string `json:"output"`
}
type semanticContextManifest struct {
	Included []semanticContextManifestEntry `json:"included"`
	Excluded []semanticContextManifestEntry `json:"excluded"`
}
type semanticContextManifestEntry struct {
	Component       string `json:"component"`
	Source          string `json:"source"`
	Reason          string `json:"reason"`
	RuntimeResolved bool   `json:"runtimeResolved,omitempty"`
}

// semanticHandoff is advisory provider output converted at the runtime input
// boundary. It intentionally excludes provider schema metadata: acceptance
// remains bound to deterministic lifecycle state and its digest.
type semanticHandoff struct {
	Encoding    string                   `json:"version"`
	Status      string                   `json:"status"`
	Summary     string                   `json:"summary"`
	Changes     []semanticHandoffChange  `json:"changes"`
	Findings    []semanticHandoffFinding `json:"findings"`
	Checks      []string                 `json:"checks"`
	Risks       []string                 `json:"risks"`
	Blockers    []string                 `json:"blockers"`
	NextActions []string                 `json:"nextActions"`
}
type semanticHandoffChange struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
}
type semanticHandoffFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

// Validate closes the durable semantic boundary without projecting through a
// provider wire type or provider contract version.
func (h semanticHandoff) Validate() error {
	if h.Encoding != semanticHandoffEncoding {
		return fmt.Errorf("unsupported semantic handoff encoding %q", h.Encoding)
	}
	if h.Status != "complete" && h.Status != "blocked" {
		return fmt.Errorf("semantic handoff status must be complete or blocked")
	}
	if h.Status == "complete" && len(h.Blockers) != 0 {
		return fmt.Errorf("complete semantic handoff cannot contain blockers")
	}
	if h.Status == "blocked" && len(h.Blockers) == 0 {
		return fmt.Errorf("blocked semantic handoff requires blockers")
	}
	if err := validSemanticHandoffText(h.Summary); err != nil {
		return fmt.Errorf("semantic handoff summary: %w", err)
	}
	for _, group := range [][]string{h.Checks, h.Risks, h.Blockers, h.NextActions} {
		if len(group) > semanticMaxHandoffCollection {
			return fmt.Errorf("semantic handoff collection exceeds %d entries", semanticMaxHandoffCollection)
		}
		for _, value := range group {
			if err := validSemanticHandoffText(value); err != nil {
				return err
			}
		}
	}
	if len(h.Changes) > semanticMaxHandoffCollection || len(h.Findings) > semanticMaxHandoffCollection {
		return fmt.Errorf("semantic handoff collection exceeds %d entries", semanticMaxHandoffCollection)
	}
	for _, change := range h.Changes {
		if !safeSemanticHandoffPath(change.Path) {
			return fmt.Errorf("semantic handoff change path is unsafe")
		}
		if err := validSemanticHandoffText(change.Path); err != nil {
			return err
		}
		if err := validSemanticHandoffText(change.Summary); err != nil {
			return err
		}
	}
	for _, finding := range h.Findings {
		if finding.Severity == "" {
			return fmt.Errorf("semantic handoff finding severity is required")
		}
		if err := validSemanticHandoffText(finding.Severity); err != nil {
			return err
		}
		if err := validSemanticHandoffText(finding.Summary); err != nil {
			return err
		}
	}
	canonical, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("encode semantic handoff: %w", err)
	}
	if len(canonical) > semanticMaxHandoffBytes {
		return fmt.Errorf("semantic handoff exceeds %d bytes", semanticMaxHandoffBytes)
	}
	return nil
}

func validSemanticHandoffText(value string) error {
	if len(value) > semanticMaxHandoffStringBytes {
		return fmt.Errorf("semantic handoff text exceeds %d bytes", semanticMaxHandoffStringBytes)
	}
	if semanticHandoffSecret.MatchString(value) || semanticHandoffCredentialSignature.MatchString(value) {
		return fmt.Errorf("semantic handoff contains secret-like material")
	}
	return nil
}

func safeSemanticHandoffPath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || path.IsAbs(value) || semanticWindowsVolumePrefix.MatchString(value) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." || component == "" {
			return false
		}
	}
	return path.Clean(value) == value
}
