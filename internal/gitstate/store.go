package gitstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Store provides durable workflow state persistence using Git refs and objects.
// Each workflow gets its own namespace under refs/agentflow/ for isolation.
type Store struct {
	Repo      Repo
	Namespace string
}

// NewStore creates a new Store for the given repository and workflow name.
// The workflow name is encoded into a deterministic Git ref namespace.
func NewStore(repo Repo, workflowName string) Store {
	// A workflow name is user supplied, while a Git ref name has a more
	// restrictive grammar. Replacing unsafe characters is not sufficient: for
	// example, "release candidate" and "release-candidate" would otherwise
	// share one state namespace. Encode every byte instead, so the mapping is
	// deterministic and injective without relying on an external state index.
	return Store{Repo: repo, Namespace: NamespaceForWorkflow(workflowName)}
}

func (s Store) ref(name string) string { return s.Namespace + "/" + strings.TrimPrefix(name, "/") }

func (s Store) Resolve(name string) (string, bool, error) {
	cmd := exec.Command("git", "-C", s.Repo.Root, "rev-parse", "--verify", "--quiet", s.ref(name))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

func (s Store) SetCommit(name, commit string) error {
	if !s.Repo.ObjectExists(commit + "^{commit}") {
		return fmt.Errorf("%s is not a commit", commit)
	}
	_, err := s.Repo.run(nil, "update-ref", "-m", "agentflow state: "+name, s.ref(name), commit)
	return err
}

func (s Store) SetJSON(name string, v any) error {
	sha, err := s.writeJSON(v)
	if err != nil {
		return err
	}
	_, err = s.Repo.run(nil, "update-ref", "-m", "agentflow state: "+name, s.ref(name), sha)
	return err
}

// SetJSONIf atomically writes v only when name still refers to expected. An
// empty expected value means the record must not exist. It returns false when
// another process changed the record before the compare-and-swap completed.
func (s Store) SetJSONIf(name string, v any, expected string) (bool, error) {
	sha, err := s.writeJSON(v)
	if err != nil {
		return false, err
	}
	if expected == "" {
		expected = strings.Repeat("0", len(sha))
	}
	_, err = s.Repo.run(nil, "update-ref", "-m", "agentflow state: "+name, s.ref(name), sha, expected)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "cannot lock ref") {
		return false, nil
	}
	return false, err
}

// DeleteIf atomically removes name only when it still refers to expected.
// This prevents a finishing process from clearing a newer owner's state.
func (s Store) DeleteIf(name, expected string) (bool, error) {
	if expected == "" {
		return false, nil
	}
	_, err := s.Repo.run(nil, "update-ref", "-d", s.ref(name), expected)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "cannot lock ref") {
		return false, nil
	}
	return false, err
}

func (s Store) writeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", s.Repo.Root, "hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("write state blob: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s Store) GetJSON(name string, dst any) (bool, error) {
	sha, ok, err := s.Resolve(name)
	if err != nil || !ok {
		return ok, err
	}
	b, err := s.Repo.run(nil, "cat-file", "blob", sha)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return false, fmt.Errorf("decode state %s: %w", name, err)
	}
	return true, nil
}

func (s Store) Delete(name string) error {
	_, err := s.Repo.run(nil, "update-ref", "-d", s.ref(name))
	return err
}

// Names returns the state record names currently present in this workflow
// namespace. It deliberately exposes names rather than objects so callers can
// distinguish an interrupted initialization from a populated workflow.
func (s Store) Names() ([]string, error) {
	b, err := s.Repo.run(nil, "for-each-ref", "--format=%(refname)", s.Namespace+"/")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ref := range strings.Split(string(b), "\n") {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		names = append(names, strings.TrimPrefix(ref, s.Namespace+"/"))
	}
	return names, nil
}

func (s Store) Reset() error {
	b, err := s.Repo.run(nil, "for-each-ref", "--format=%(refname)", s.Namespace+"/")
	if err != nil {
		return err
	}
	for _, ref := range strings.Split(string(b), "\n") {
		if ref == "" {
			continue
		}
		if name := strings.TrimPrefix(ref, s.Namespace+"/"); name == DescriptorRecord || name == "owner" {
			// Observational discovery metadata survives an explicit acceptance
			// reset so local logs remain addressable and status can report the
			// namespace as uninitialized. It is rebuilt on the next run.
			continue
		}
		if _, err := s.Repo.run(nil, "update-ref", "-d", ref); err != nil {
			return err
		}
	}
	return nil
}
