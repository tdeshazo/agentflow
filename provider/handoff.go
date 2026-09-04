package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

const (
	HandoffVersionV1      = "agentflow.dev/handoff/v1"
	MaxHandoffBytes       = 12 * 1024
	maxHandoffStringBytes = 2 * 1024
	maxHandoffCollection  = 50
)

// Handoff is provider output intended for a direct dependent. It is advisory:
// deterministic validation and the phase marker remain acceptance evidence.
type Handoff struct {
	Version     string           `json:"version"`
	Status      string           `json:"status"`
	Summary     string           `json:"summary"`
	Changes     []HandoffChange  `json:"changes"`
	Findings    []HandoffFinding `json:"findings"`
	Checks      []string         `json:"checks"`
	Risks       []string         `json:"risks"`
	Blockers    []string         `json:"blockers"`
	NextActions []string         `json:"nextActions"`
}

type HandoffChange struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
}
type HandoffFinding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

// HandoffRequest tells an adapter to require the portable schema. It does not
// make provider prose acceptance evidence.
type HandoffRequest struct {
	Version string `json:"version"`
}

var (
	handoffSecret              = regexp.MustCompile(`(?i)(api[_-]?key|(?:access[_-]?)?token|secret|password|private[_-]?key)\s*[:=]`)
	handoffCredentialSignature = regexp.MustCompile(`(?i)(?:\bbearer\s+[a-z0-9._~+/=-]+|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|\b(?:gh[pousr]_|github_pat_|xox[baprs]-|sk-(?:proj-)?|AKIA)[a-z0-9_-]+)`)
	windowsVolumePrefix        = regexp.MustCompile(`^[A-Za-z]:`)
)

func ParseHandoff(raw []byte) (Handoff, error) {
	return ParseHandoffWithCredentials(raw, nil)
}

// ParseHandoffWithCredentials validates decoded handoff strings against the
// credential values authorized for this invocation. Decoding first prevents
// JSON escapes from bypassing exact-value checks.
func ParseHandoffWithCredentials(raw []byte, credentials []string) (Handoff, error) {
	if len(raw) == 0 || len(raw) > MaxHandoffBytes {
		return Handoff{}, fmt.Errorf("handoff must be between 1 and %d bytes", MaxHandoffBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var h Handoff
	if err := dec.Decode(&h); err != nil {
		return Handoff{}, fmt.Errorf("decode handoff: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Handoff{}, fmt.Errorf("handoff contains trailing data")
	}
	if err := h.Validate(); err != nil {
		return Handoff{}, err
	}
	for _, value := range handoffStrings(h) {
		for _, credential := range credentials {
			if credential != "" && strings.Contains(value, credential) {
				return Handoff{}, fmt.Errorf("handoff contains authorized credential material")
			}
		}
	}
	return h, nil
}

func (h Handoff) Validate() error {
	if h.Version != HandoffVersionV1 {
		return fmt.Errorf("unsupported handoff version %q", h.Version)
	}
	if h.Status != "complete" && h.Status != "blocked" {
		return fmt.Errorf("handoff status must be complete or blocked")
	}
	if h.Status == "complete" && len(h.Blockers) != 0 {
		return fmt.Errorf("complete handoff cannot contain blockers")
	}
	if h.Status == "blocked" && len(h.Blockers) == 0 {
		return fmt.Errorf("blocked handoff requires blockers")
	}
	if err := validHandoffText(h.Summary); err != nil {
		return fmt.Errorf("handoff summary: %w", err)
	}
	for _, group := range [][]string{h.Checks, h.Risks, h.Blockers, h.NextActions} {
		if len(group) > maxHandoffCollection {
			return fmt.Errorf("handoff collection exceeds %d entries", maxHandoffCollection)
		}
		for _, value := range group {
			if err := validHandoffText(value); err != nil {
				return err
			}
		}
	}
	if len(h.Changes) > maxHandoffCollection || len(h.Findings) > maxHandoffCollection {
		return fmt.Errorf("handoff collection exceeds %d entries", maxHandoffCollection)
	}
	for _, change := range h.Changes {
		if !safeHandoffPath(change.Path) {
			return fmt.Errorf("handoff change path is unsafe")
		}
		if err := validHandoffText(change.Path); err != nil {
			return err
		}
		if err := validHandoffText(change.Summary); err != nil {
			return err
		}
	}
	for _, finding := range h.Findings {
		if finding.Severity == "" {
			return fmt.Errorf("handoff finding severity is required")
		}
		if err := validHandoffText(finding.Severity); err != nil {
			return err
		}
		if err := validHandoffText(finding.Summary); err != nil {
			return err
		}
	}
	canonical, _ := json.Marshal(h)
	if len(canonical) > MaxHandoffBytes {
		return fmt.Errorf("handoff exceeds %d bytes", MaxHandoffBytes)
	}
	return nil
}
func validHandoffText(value string) error {
	if len(value) > maxHandoffStringBytes {
		return fmt.Errorf("handoff text exceeds %d bytes", maxHandoffStringBytes)
	}
	if handoffSecret.MatchString(value) || handoffCredentialSignature.MatchString(value) {
		return fmt.Errorf("handoff contains secret-like material")
	}
	return nil
}

func safeHandoffPath(value string) bool {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, `\`) || path.IsAbs(value) || windowsVolumePrefix.MatchString(value) {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." || component == "" {
			return false
		}
	}
	return path.Clean(value) == value
}

func handoffStrings(h Handoff) []string {
	values := []string{h.Version, h.Status, h.Summary}
	for _, change := range h.Changes {
		values = append(values, change.Path, change.Summary)
	}
	for _, finding := range h.Findings {
		values = append(values, finding.Severity, finding.Summary)
	}
	values = append(values, h.Checks...)
	values = append(values, h.Risks...)
	values = append(values, h.Blockers...)
	values = append(values, h.NextActions...)
	return values
}

// HandoffJSONSchema is passed to providers with native structured-output
// support. Engine validation remains authoritative.
func HandoffJSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"version", "status", "summary", "changes", "findings", "checks", "risks", "blockers", "nextActions"}, "properties": map[string]any{"version": map[string]any{"const": HandoffVersionV1}, "status": map[string]any{"enum": []string{"complete", "blocked"}}, "summary": map[string]any{"type": "string", "maxLength": maxHandoffStringBytes}, "changes": map[string]any{"type": "array", "maxItems": maxHandoffCollection, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"path", "summary"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}}}}, "findings": map[string]any{"type": "array", "maxItems": maxHandoffCollection, "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"severity", "summary"}, "properties": map[string]any{"severity": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}}}}, "checks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "risks": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "blockers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "nextActions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
}
