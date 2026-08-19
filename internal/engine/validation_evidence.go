package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

const (
	validationEvidenceVersion   = 1
	validationEvidenceAlgorithm = "sha256"
	maxValidationFailureOutput  = 8192
)

// ValidationEvidence is deliberately a small success record, not a general
// artifact or result contract. All workspace, input, policy, and definition
// material is represented by digests so durable state cannot disclose the
// command environment, prompts, or resolved parameter values.
type ValidationEvidence struct {
	Version              int    `json:"version"`
	Algorithm            string `json:"algorithm"`
	Key                  string `json:"key"`
	Validation           string `json:"validation"`
	RunIdentityDigest    string `json:"run_identity_digest"`
	DefinitionDigest     string `json:"definition_digest"`
	ResolvedInputsDigest string `json:"resolved_inputs_digest"`
	WorkspaceDigest      string `json:"workspace_digest"`
	PolicyDigest         string `json:"policy_digest"`
	DependenciesDigest   string `json:"dependencies_digest"`
	PhaseDigest          string `json:"phase_digest"`
}

type validationEvidenceKey struct {
	Version              int    `json:"version"`
	Algorithm            string `json:"algorithm"`
	Validation           string `json:"validation"`
	RunIdentityDigest    string `json:"run_identity_digest"`
	DefinitionDigest     string `json:"definition_digest"`
	ResolvedInputsDigest string `json:"resolved_inputs_digest"`
	WorkspaceDigest      string `json:"workspace_digest"`
	PolicyDigest         string `json:"policy_digest"`
	DependenciesDigest   string `json:"dependencies_digest"`
	PhaseDigest          string `json:"phase_digest"`
}

type resolvedValidationToolUse struct {
	Name    string                 `json:"name"`
	If      string                 `json:"if,omitempty"`
	Enabled bool                   `json:"enabled"`
	With    workflow.ToolArguments `json:"with"`
	Command string                 `json:"command,omitempty"`
	Policy  string                 `json:"policy,omitempty"`
}

type validationFileIdentity struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Digest  string `json:"digest,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

var sensitiveFailureKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|authorization|cookie|credential|private[_-]?key)`)
var environmentLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func (e *Engine) validationEvidenceKey(name string, v workflow.Validation, p *workflow.Phase) (validationEvidenceKey, bool, error) {
	cacheable, err := e.validationCacheable(v)
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	identity, err := e.expectedRunIdentity()
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	runIdentityDigest, err := digestCanonicalJSON(identity)
	if err != nil {
		return validationEvidenceKey{}, false, err
	}

	tools := map[string]workflow.Tool{}
	resolved := make([]resolvedValidationToolUse, 0, len(v.Steps)+len(v.OnFailure.Then))
	for _, use := range append(append([]workflow.ToolUse{}, v.Steps...), v.OnFailure.Then...) {
		t, ok := e.Workflow.Spec.Tools[use.Uses]
		if !ok {
			return validationEvidenceKey{}, false, fmt.Errorf("unknown tool %q", use.Uses)
		}
		tools[use.Uses] = t
		enabled := true
		if use.If != "" {
			enabled, err = e.bool(p, use.If)
			if err != nil {
				return validationEvidenceKey{}, false, fmt.Errorf("tool %s condition: %w", use.Uses, err)
			}
		}
		with := use.With
		if with.Path != "" {
			with.Path, err = e.context(p).Expand(with.Path)
			if err != nil {
				return validationEvidenceKey{}, false, err
			}
		}
		if with.Regex != "" {
			with.Regex, err = e.context(p).Expand(with.Regex)
			if err != nil {
				return validationEvidenceKey{}, false, err
			}
		}
		command, policy := t.Command, t.Policy
		if command != "" {
			command, err = e.context(p).Expand(command)
			if err != nil {
				return validationEvidenceKey{}, false, err
			}
		}
		if policy != "" {
			policy, err = e.context(p).Expand(policy)
			if err != nil {
				return validationEvidenceKey{}, false, err
			}
		}
		resolved = append(resolved, resolvedValidationToolUse{
			Name: use.Uses, If: use.If, Enabled: enabled, With: with,
			Command: command, Policy: policy,
		})
	}
	definitionDigest, err := digestCanonicalJSON(struct {
		Validation workflow.Validation      `json:"validation"`
		Tools      map[string]workflow.Tool `json:"tools"`
	}{Validation: v, Tools: tools})
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	resolvedInputsDigest, err := digestCanonicalJSON(struct {
		ParametersDigest string                      `json:"parameters_digest"`
		ExecutionDigest  string                      `json:"execution_digest"`
		Uses             []resolvedValidationToolUse `json:"uses"`
	}{identity.ParametersDigest, identity.ExecutionDigest, resolved})
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	workspaceDigest, dependenciesDigest, err := e.validationWorkspaceIdentity(v, p)
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	policyDigest, err := digestCanonicalJSON(struct {
		MutationPolicy workflow.MutationPolicy  `json:"mutation_policy"`
		Lineage        workflow.StateLineage    `json:"lineage"`
		Resume         workflow.StateResume     `json:"resume"`
		Failure        workflow.FailurePolicy   `json:"failure"`
		Lifecycle      workflow.LifecyclePolicy `json:"lifecycle"`
	}{e.Workflow.Spec.Workspace.MutationPolicy, e.Workflow.Spec.State.Lineage, e.Workflow.Spec.State.Resume, v.OnFailure, e.Workflow.Spec.Lifecycle})
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	phaseDigest, err := digestCanonicalJSON(validationPhaseIdentity(p, e.runtimeOwnsPhaseLifecycle(p)))
	if err != nil {
		return validationEvidenceKey{}, false, err
	}
	key := validationEvidenceKey{
		Version: validationEvidenceVersion, Algorithm: validationEvidenceAlgorithm,
		Validation: name, RunIdentityDigest: runIdentityDigest,
		DefinitionDigest: definitionDigest, ResolvedInputsDigest: resolvedInputsDigest,
		WorkspaceDigest: workspaceDigest, PolicyDigest: policyDigest,
		DependenciesDigest: dependenciesDigest, PhaseDigest: phaseDigest,
	}
	return key, cacheable, nil
}

type phaseEvidenceIdentity struct {
	ID              string `json:"id,omitempty"`
	Kind            string `json:"kind,omitempty"`
	RequiresChange  bool   `json:"requires_change,omitempty"`
	AdvanceProgress bool   `json:"advance_progress,omitempty"`
	RuntimeOwned    bool   `json:"runtime_owned"`
}

func validationPhaseIdentity(p *workflow.Phase, runtimeOwned bool) phaseEvidenceIdentity {
	if p == nil {
		return phaseEvidenceIdentity{RuntimeOwned: runtimeOwned}
	}
	return phaseEvidenceIdentity{ID: p.ID, Kind: p.Kind, RequiresChange: p.RequiresChange, AdvanceProgress: p.AdvanceProgress, RuntimeOwned: runtimeOwned}
}

func (e *Engine) validationCacheable(v workflow.Validation) (bool, error) {
	uses := append(append([]workflow.ToolUse{}, v.Steps...), v.OnFailure.Then...)
	for _, use := range uses {
		t, ok := e.Workflow.Spec.Tools[use.Uses]
		if !ok {
			return false, fmt.Errorf("unknown tool %q", use.Uses)
		}
		switch t.Type {
		case "workspace-policy", "shell", "file-regex", "markdown-checklist-progress":
			if t.Type == "shell" && t.MutatesWorkspace {
				return false, nil
			}
		case "git-checkpoint":
			return false, nil
		default:
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) validationWorkspaceIdentity(v workflow.Validation, p *workflow.Phase) (string, string, error) {
	patterns := append([]string{}, v.Dependencies...)
	uses := append(append([]workflow.ToolUse{}, v.Steps...), v.OnFailure.Then...)
	for _, use := range uses {
		if use.With.Path != "" {
			patterns = append(patterns, use.With.Path)
		}
	}
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	resolved := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		value, err := e.context(p).Expand(pattern)
		if err != nil {
			return "", "", err
		}
		value, err = workspaceRelativePattern(e.Repo.Root, value)
		if err != nil {
			return "", "", err
		}
		resolved = append(resolved, value)
	}
	files, err := e.Repo.PresentFiles()
	if err != nil {
		return "", "", err
	}
	outputPatterns := []string{}
	for _, use := range uses {
		if tool, ok := e.Workflow.Spec.Tools[use.Uses]; ok && tool.Capture.Log != "" {
			// Capture logs are validation output, not validation input. Treat the
			// invocation placeholder as a glob before expansion so old process
			// outputs do not poison a later equivalent lookup.
			logTemplate := strings.ReplaceAll(tool.Capture.Log, "{{ invocation.id }}", "*")
			logPattern, expandErr := e.context(p).Expand(logTemplate)
			if expandErr == nil {
				outputPatterns = append(outputPatterns, logPattern)
			}
		}
	}
	entries := make([]validationFileIdentity, 0)
	seen := map[string]bool{}
	for _, pattern := range resolved {
		matched := false
		for _, file := range files {
			if !dependencyMatches(pattern, file) || matchesAnyDependency(outputPatterns, file) {
				continue
			}
			matched = true
			if seen[file] {
				continue
			}
			identity, err := hashValidationFile(filepath.Join(e.Repo.Root, filepath.FromSlash(file)))
			if err != nil {
				return "", "", err
			}
			identity.Pattern = pattern
			identity.Path = file
			entries = append(entries, identity)
			seen[file] = true
		}
		if !matched {
			entries = append(entries, validationFileIdentity{Pattern: pattern, Missing: true})
		}
	}
	workspaceDigest, err := digestCanonicalJSON(entries)
	if err != nil {
		return "", "", err
	}
	dependenciesDigest, err := digestCanonicalJSON(resolved)
	if err != nil {
		return "", "", err
	}
	return workspaceDigest, dependenciesDigest, nil
}

func workspaceRelativePattern(root, value string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return "", fmt.Errorf("validation dependency must not be empty")
	}
	if filepath.IsAbs(value) {
		rel, err := filepath.Rel(root, value)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("validation dependency must be workspace-relative")
		}
		value = filepath.ToSlash(rel)
	}
	value = strings.TrimPrefix(value, "./")
	if value == "" {
		value = "**"
	}
	return value, nil
}

func dependencyMatches(pattern, file string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	file = strings.TrimPrefix(filepath.ToSlash(file), "./")
	if pattern == "**" || pattern == "**/*" || pattern == "." {
		return true
	}
	if strings.HasSuffix(pattern, "/**") && strings.HasPrefix(file, strings.TrimSuffix(pattern, "**")) {
		return true
	}
	if strings.HasSuffix(pattern, "/") && strings.HasPrefix(file, pattern) {
		return true
	}
	return globMatch(pattern, file)
}

func matchesAnyDependency(patterns []string, file string) bool {
	for _, pattern := range patterns {
		pattern = strings.ReplaceAll(pattern, "{{ invocation.id }}", "*")
		if dependencyMatches(pattern, file) {
			return true
		}
	}
	return false
}

func hashValidationFile(path string) (validationFileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return validationFileIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return validationFileIdentity{}, err
		}
		return validationFileIdentity{Digest: digestBytes([]byte("symlink:" + target)), Mode: uint32(info.Mode())}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return validationFileIdentity{}, err
	}
	return validationFileIdentity{Digest: digestBytes(b), Mode: uint32(info.Mode())}, nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (e *Engine) validationEvidenceRecord(key validationEvidenceKey) (string, error) {
	digest, err := digestCanonicalJSON(key)
	if err != nil {
		return "", err
	}
	// The run identity digest is part of the record path as well as the
	// content-addressed key. This prevents two concurrent invocations of the
	// same workflow from sharing evidence merely because their gate text agrees.
	return "validation-evidence/" + key.RunIdentityDigest + "/" + digest, nil
}

func (e *Engine) loadValidationEvidence(key validationEvidenceKey) (bool, error) {
	record, err := e.validationEvidenceRecord(key)
	if err != nil {
		return false, err
	}
	var evidence ValidationEvidence
	ok, err := e.Store.GetJSON(record, &evidence)
	if err != nil || !ok {
		return ok, err
	}
	want, err := digestCanonicalJSON(key)
	if err != nil {
		return false, err
	}
	return evidence.Version == validationEvidenceVersion && evidence.Algorithm == validationEvidenceAlgorithm && evidence.Key == want && evidence.Validation == key.Validation, nil
}

func (e *Engine) persistValidationEvidence(key validationEvidenceKey) error {
	record, err := e.validationEvidenceRecord(key)
	if err != nil {
		return err
	}
	digest, err := digestCanonicalJSON(key)
	if err != nil {
		return err
	}
	evidence := ValidationEvidence{
		Version: key.Version, Algorithm: key.Algorithm, Key: digest,
		Validation: key.Validation, RunIdentityDigest: key.RunIdentityDigest,
		DefinitionDigest: key.DefinitionDigest, ResolvedInputsDigest: key.ResolvedInputsDigest,
		WorkspaceDigest: key.WorkspaceDigest, PolicyDigest: key.PolicyDigest,
		DependenciesDigest: key.DependenciesDigest, PhaseDigest: key.PhaseDigest,
	}
	return e.Store.SetJSON(record, evidence)
}

func boundedFailureOutput(err error) string {
	if err == nil {
		return ""
	}
	lines := strings.Split(err.Error(), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if environmentLine.MatchString(trimmed) || sensitiveFailureKey.MatchString(trimmed) {
			if index := strings.IndexAny(trimmed, "=:"); index >= 0 {
				lines[i] = trimmed[:index+1] + "[redacted]"
				continue
			}
			lines[i] = "[redacted sensitive diagnostic]"
		}
	}
	output := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(output) > maxValidationFailureOutput {
		output = output[:maxValidationFailureOutput] + "\n[validation output truncated]"
	}
	return output
}
