package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

const maxIntegrityPathsPerCategory = gitstate.MaxIntegrityPathsPerCategory

// UnmarshalJSON preserves recovery from integrity baselines written before
// path-level manifests were introduced. Legacy values are aggregate digest
// strings; current values retain that aggregate under an explicit field.
func (b *IntegrityBaseline) UnmarshalJSON(data []byte) error {
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	baseline := make(IntegrityBaseline, len(encoded))
	for id, raw := range encoded {
		var legacy string
		if err := json.Unmarshal(raw, &legacy); err == nil {
			baseline[id] = IntegrityRuleBaseline{Aggregate: legacy}
			continue
		}
		var current IntegrityRuleBaseline
		if err := json.Unmarshal(raw, &current); err != nil {
			return fmt.Errorf("integrity rule %q baseline: %w", id, err)
		}
		if current.Aggregate == "" {
			return fmt.Errorf("integrity rule %q baseline has no aggregate digest", id)
		}
		for path := range current.Paths {
			safe, err := safeRelativeIntegrityPath(path)
			if err != nil || safe != path {
				return fmt.Errorf("integrity rule %q baseline has unsafe path %q", id, path)
			}
		}
		baseline[id] = current
	}
	*b = baseline
	return nil
}

func (e *Engine) phaseByID(id string) (*workflow.Phase, error) {
	for i := range e.Workflow.Spec.Phases {
		if e.Workflow.Spec.Phases[i].ID == id {
			return &e.Workflow.Spec.Phases[i], nil
		}
	}
	return nil, fmt.Errorf("unknown phase %q", id)
}
func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
func errorOutput(err error) string {
	return boundedFailureOutput(err)
}

func (e *Engine) validCommitMarker(name string) (bool, string, error) {
	sha, ok, err := e.Store.Resolve(name)
	if err != nil || !ok {
		return false, "", err
	}
	if !e.Repo.ObjectExists(sha + "^{commit}") {
		return false, sha, nil
	}
	if !e.Repo.IsAncestor(sha, "HEAD") {
		return false, sha, nil
	}
	return true, sha, nil
}

func (e *Engine) lifecycleConfigured() bool {
	l := e.Workflow.Spec.Lifecycle
	return l.Policy != "" || l.Validation != "" || l.Checkpoint != ""
}

// runtimeOwnsPhaseLifecycle selects the compact lifecycle contract. An
// entirely procedural v1alpha1 phase remains on the legacy action path so old
// documents keep their meaning; phases without those actions receive the safe
// runtime defaults even when spec.lifecycle is omitted.
func (e *Engine) runtimeOwnsPhaseLifecycle(p *workflow.Phase) bool {
	if e.lifecycleConfigured() {
		return true
	}
	if p == nil {
		return false
	}
	if p.AdvanceProgress || len(p.Bookkeeping) > 0 {
		// Engine-owned transitions have a fixed ordering relative to validation,
		// durable active state, and checkpointing; they cannot be delegated to a
		// legacy procedural action list.
		return true
	}
	return len(e.Workflow.Spec.PhaseDefaults.Before) == 0 &&
		len(e.Workflow.Spec.PhaseDefaults.After) == 0 &&
		len(p.After) == 0
}

func (e *Engine) phaseValidation(p *workflow.Phase) string {
	if p != nil && p.Validation != "" {
		return p.Validation
	}
	return e.Workflow.Spec.Lifecycle.Validation
}

func (e *Engine) phaseCheckpoint(p *workflow.Phase) string {
	if p != nil && len(p.After) == 0 {
		return e.Workflow.Spec.Lifecycle.Checkpoint
	}
	return ""
}

// assertLineage is shared by actor, tool, checkpoint, recovery, and
// acceptance boundaries. strict is used by the safe runtime lifecycle even
// when a legacy document did not repeat every lineage flag.
func (e *Engine) assertLineage(strict bool) error {
	base, ok, err := e.Store.Resolve(e.baseRecord())
	if err != nil {
		return err
	}
	if !ok {
		if strict {
			return fmt.Errorf("workflow base state is missing")
		}
		return nil
	}
	requireBase := strict || e.Workflow.Spec.State.Lineage.RequireBaseCommitExists ||
		e.Workflow.Spec.State.Resume.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.State.Lineage.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireBaseIsAncestorOfHead
	if requireBase && !e.Repo.ObjectExists(base+"^{commit}") {
		return fmt.Errorf("saved base no longer exists: %s", base)
	}
	if strict || e.Workflow.Spec.State.Resume.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.State.Lineage.RequireBaseIsAncestorOfHead ||
		e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireBaseIsAncestorOfHead {
		if !e.Repo.IsAncestor(base, "HEAD") {
			return fmt.Errorf("HEAD no longer descends from workflow base %s", base)
		}
	}
	requireBranch := strict || e.Workflow.Spec.State.Lineage.RequireSameNamedBranch ||
		e.Workflow.Spec.State.Resume.RequireSameBranch ||
		e.Workflow.Spec.Workspace.MutationPolicy.Lineage.RequireSameBranchAsState
	if !requireBranch {
		return nil
	}
	var savedBranch string
	branchOK, err := e.Store.GetJSON(e.branchRecord(), &savedBranch)
	if err != nil {
		return err
	}
	if !branchOK || savedBranch == "" {
		return fmt.Errorf("workflow branch state is missing")
	}
	current, err := e.Repo.Branch()
	if err != nil {
		return fmt.Errorf("workflow requires its initialized named branch; detached HEAD is not supported")
	}
	if current != savedBranch {
		return fmt.Errorf("current branch %q differs from workflow branch %q", current, savedBranch)
	}
	return nil
}

func (e *Engine) implementationDirtyFiles() ([]string, error) {
	files, err := e.Repo.DirtyFiles()
	if err != nil {
		return nil, err
	}
	return e.filterIgnored(files), nil
}
func (e *Engine) changedImplementationFiles() ([]string, error) {
	base, ok, err := e.Store.Resolve(e.baseRecord())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	files, err := e.Repo.ChangedFilesSince(base)
	if err != nil {
		return nil, err
	}
	return e.filterIgnored(files), nil
}
func (e *Engine) filterIgnored(files []string) []string {
	patterns := e.ignoredPatterns()
	var out []string
	for _, f := range files {
		if !matchesAny(patterns, f) {
			out = append(out, f)
		}
	}
	return out
}
func (e *Engine) ignoredPatterns() []string {
	var out []string
	patterns := append([]string{}, e.Workflow.Spec.Workspace.LocalControl.Ignored...)
	patterns = append(patterns, e.Workflow.Spec.Workspace.MutationPolicy.IgnoredControlFiles...)
	for _, p := range patterns {
		expanded, err := e.context(nil).Expand(p)
		if err != nil {
			continue
		}
		if filepath.IsAbs(expanded) {
			if rel, err := filepath.Rel(e.Repo.Root, expanded); err == nil {
				expanded = filepath.ToSlash(rel)
			}
		}
		out = append(out, expanded)
	}
	return out
}

func (e *Engine) assertScope() error {
	if err := e.assertLineage(false); err != nil {
		return err
	}
	if err := e.assertIntegrity(); err != nil {
		return err
	}
	return e.assertAllowedScope()
}

func (e *Engine) assertAllowedScope() error {
	files, err := e.changedImplementationFiles()
	if err != nil {
		return err
	}
	// Check immutable project boundaries first. A protected file is rejected as
	// a protected-file violation even though it is also outside the ordinary
	// mutation allowlist; this keeps the policy's reason durable and explicit.
	allowed := e.Workflow.Spec.Workspace.MutationPolicy.Allowed
	for _, f := range files {
		if !matchesAny(allowed, f) {
			return &safetyViolation{err: fmt.Errorf("out-of-scope file changed: %s", f)}
		}
	}
	return nil
}

// assertMutationBoundary centralizes the invariants that must hold whenever
// the runtime is about to trust or persist workspace-derived acceptance
// evidence. It never removes or resets user changes.
func (e *Engine) assertMutationBoundary(requireClean, strictLineage bool) error {
	if err := e.assertLineage(strictLineage); err != nil {
		return &safetyViolation{err: err}
	}
	if err := e.assertIntegrity(); err != nil {
		return err
	}
	if err := e.assertAllowedScope(); err != nil {
		return err
	}
	if !requireClean {
		return nil
	}
	dirty, err := e.implementationDirtyFiles()
	if err != nil {
		return err
	}
	if len(dirty) > 0 {
		return fmt.Errorf("implementation workspace is dirty: %s", strings.Join(dirty, ", "))
	}
	return nil
}

func (e *Engine) computeIntegrity() (IntegrityBaseline, error) {
	return e.computeIntegrityBaseline(true)
}

func (e *Engine) computeIntegrityBaseline(requireMatches bool) (IntegrityBaseline, error) {
	rules := e.Workflow.Spec.Workspace.MutationPolicy.Integrity
	out := IntegrityBaseline{}
	for _, r := range rules {
		baseline, err := e.integrityRuleBaseline(r, requireMatches)
		if err != nil {
			return nil, fmt.Errorf("integrity %s: %w", r.ID, err)
		}
		out[r.ID] = baseline
	}
	return out, nil
}

func (e *Engine) assertIntegrity() error {
	var baseline IntegrityBaseline
	ok, err := e.Store.GetJSON(e.integrityRecord(), &baseline)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	current, err := e.computeIntegrityBaseline(false)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(baseline))
	for id := range baseline {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		want := baseline[id]
		got, exists := current[id]
		if !exists || got.Aggregate != want.Aggregate {
			violation := &safetyViolation{err: fmt.Errorf("protected integrity rule %s changed", id)}
			if want.Paths != nil {
				violation.integrityViolation = diffIntegrityManifests(id, want.Paths, got.Paths)
			}
			return violation
		}
	}
	return nil
}

func (e *Engine) integrityHash(rule workflow.IntegrityRule) (string, error) {
	baseline, err := e.integrityRuleBaseline(rule, true)
	if err != nil {
		return "", err
	}
	return baseline.Aggregate, nil
}

func (e *Engine) integrityRuleBaseline(rule workflow.IntegrityRule, requireMatches bool) (IntegrityRuleBaseline, error) {
	filesInWorkspace, err := e.Repo.IntegrityFiles()
	if err != nil {
		return IntegrityRuleBaseline{}, err
	}
	var files []string
	for _, f := range filesInWorkspace {
		if matchesAny(rule.Paths, f) && !matchesAny(rule.Exclude, f) {
			safe, err := safeRelativeIntegrityPath(f)
			if err != nil {
				return IntegrityRuleBaseline{}, err
			}
			files = append(files, safe)
		}
	}
	sort.Strings(files)
	if requireMatches && len(files) == 0 {
		return IntegrityRuleBaseline{}, fmt.Errorf("paths matched no workspace files")
	}
	h := sha256.New()
	pathDigests := make(map[string]string, len(files))
	for _, f := range files {
		path := filepath.Join(e.Repo.Root, f)
		info, err := os.Lstat(path)
		if err != nil {
			return IntegrityRuleBaseline{}, err
		}
		var b []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return IntegrityRuleBaseline{}, err
			}
			// Hash the link object, not the target. Following a workspace symlink
			// could escape the repository and turn external state into acceptance
			// authority without declaring that external boundary.
			b = []byte("symlink\x00" + target)
		} else {
			b, err = os.ReadFile(path)
			if err != nil {
				return IntegrityRuleBaseline{}, err
			}
		}
		if rule.Mode == "normalized-hash" && rule.Normalize.Command != "" && info.Mode()&os.ModeSymlink == 0 {
			cmd := exec.Command("sh", "-c", rule.Normalize.Command)
			cmd.Dir = e.Repo.Root
			cmd.Stdin = bytes.NewReader(b)
			b, err = cmd.Output()
			if err != nil {
				return IntegrityRuleBaseline{}, fmt.Errorf("normalize %s: %w", f, err)
			}
		}
		pathDigest := sha256.Sum256(b)
		pathDigests[f] = hex.EncodeToString(pathDigest[:])
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return IntegrityRuleBaseline{
		Aggregate: hex.EncodeToString(h.Sum(nil)),
		Paths:     pathDigests,
	}, nil
}

func safeRelativeIntegrityPath(path string) (string, error) {
	if path == "" || !utf8.ValidString(path) || filepath.IsAbs(path) || !filepath.IsLocal(path) {
		return "", fmt.Errorf("unsafe integrity path %q", path)
	}
	path = filepath.ToSlash(filepath.Clean(path))
	for _, r := range path {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("unsafe integrity path %q", path)
		}
	}
	return path, nil
}

func diffIntegrityManifests(rule string, before, after map[string]string) *gitstate.IntegrityViolation {
	violation := &gitstate.IntegrityViolation{
		IntegrityRule: rule,
		Changed:       []string{},
		Added:         []string{},
		Removed:       []string{},
	}
	paths := make([]string, 0, len(before)+len(after))
	seen := make(map[string]struct{}, len(before)+len(after))
	for path := range before {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range after {
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		beforeDigest, existed := before[path]
		afterDigest, exists := after[path]
		switch {
		case existed && exists && beforeDigest != afterDigest:
			violation.Changed = appendBoundedIntegrityPath(violation.Changed, path)
		case !existed && exists:
			violation.Added = appendBoundedIntegrityPath(violation.Added, path)
		case existed && !exists:
			violation.Removed = appendBoundedIntegrityPath(violation.Removed, path)
		}
	}
	return violation
}

func appendBoundedIntegrityPath(paths []string, path string) []string {
	if len(paths) >= maxIntegrityPathsPerCategory {
		return paths
	}
	return append(paths, path)
}
