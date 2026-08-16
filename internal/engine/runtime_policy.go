package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

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
	if err == nil {
		return ""
	}
	return err.Error()
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
	files, err := e.changedImplementationFiles()
	if err != nil {
		return err
	}
	// Check immutable project boundaries first. A protected file is rejected as
	// a protected-file violation even though it is also outside the ordinary
	// mutation allowlist; this keeps the policy's reason durable and explicit.
	if err := e.assertIntegrity(); err != nil {
		return err
	}
	allowed := e.Workflow.Spec.Workspace.MutationPolicy.Allowed
	for _, f := range files {
		if !matchesAny(allowed, f) {
			return &safetyViolation{err: fmt.Errorf("out-of-scope file changed: %s", f)}
		}
	}
	return nil
}

func (e *Engine) computeIntegrity() (IntegrityBaseline, error) {
	rules := e.Workflow.Spec.Workspace.MutationPolicy.Integrity
	out := IntegrityBaseline{}
	for _, r := range rules {
		h, err := e.integrityHash(r)
		if err != nil {
			return nil, fmt.Errorf("integrity %s: %w", r.ID, err)
		}
		out[r.ID] = h
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
	current, err := e.computeIntegrity()
	if err != nil {
		return err
	}
	for id, want := range baseline {
		if current[id] != want {
			return &safetyViolation{err: fmt.Errorf("protected integrity rule %s changed", id)}
		}
	}
	return nil
}

func (e *Engine) integrityHash(rule workflow.IntegrityRule) (string, error) {
	filesInWorkspace, err := e.Repo.PresentFiles()
	if err != nil {
		return "", err
	}
	var files []string
	for _, f := range filesInWorkspace {
		if matchesAny(rule.Paths, f) && !matchesAny(rule.Exclude, f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(e.Repo.Root, f))
		if err != nil {
			return "", err
		}
		if rule.Mode == "normalized-hash" && rule.Normalize.Command != "" {
			cmd := exec.Command("sh", "-c", rule.Normalize.Command)
			cmd.Dir = e.Repo.Root
			cmd.Stdin = bytes.NewReader(b)
			b, err = cmd.Output()
			if err != nil {
				return "", fmt.Errorf("normalize %s: %w", f, err)
			}
		}
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
