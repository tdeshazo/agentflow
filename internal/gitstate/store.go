package gitstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var unsafeRef = regexp.MustCompile(`[^A-Za-z0-9._/-]+`)

type Store struct {
	Repo      Repo
	Namespace string
}

func NewStore(repo Repo, workflowName string) Store {
	name := unsafeRef.ReplaceAllString(workflowName, "-")
	name = strings.Trim(name, "/.-")
	return Store{Repo: repo, Namespace: "refs/agentflow/" + name}
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
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", s.Repo.Root, "hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("write state blob: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	_, err = s.Repo.run(nil, "update-ref", "-m", "agentflow state: "+name, s.ref(name), sha)
	return err
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

func (s Store) Reset() error {
	b, err := s.Repo.run(nil, "for-each-ref", "--format=%(refname)", s.Namespace+"/")
	if err != nil {
		return err
	}
	for _, ref := range strings.Split(string(b), "\n") {
		if ref == "" {
			continue
		}
		if _, err := s.Repo.run(nil, "update-ref", "-d", ref); err != nil {
			return err
		}
	}
	return nil
}
