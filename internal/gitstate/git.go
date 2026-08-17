// Package gitstate provides Git-based durable state management for workflows.
// It uses Git objects and refs to durably store workflow execution state
// and provides repository operations needed by the engine.
package gitstate

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Repo represents a Git repository and provides operations for repository interaction.
type Repo struct{ Root string }

// run executes a git command in the repository with optional stdin input.
func (r Repo) run(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (r Repo) IsRepository() bool {
	_, err := r.run(nil, "rev-parse", "--git-dir")
	return err == nil
}

// GitPath resolves a repository-local runtime path through Git. This matters
// for linked worktrees, where the effective Git directory is not necessarily
// the worktree's .git directory.
func (r Repo) GitPath(path string) (string, error) {
	b, err := r.run(nil, "rev-parse", "--git-path", path)
	if err != nil {
		return "", err
	}
	resolved := strings.TrimSpace(string(b))
	if !filepath.IsAbs(resolved) {
		resolved, err = filepath.Abs(filepath.Join(r.Root, resolved))
		if err != nil {
			return "", err
		}
	}
	return filepath.Clean(resolved), nil
}

func (r Repo) Head() (string, error) {
	b, err := r.run(nil, "rev-parse", "HEAD")
	return strings.TrimSpace(string(b)), err
}

func (r Repo) Branch() (string, error) {
	b, err := r.run(nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (r Repo) ObjectExists(obj string) bool {
	_, err := r.run(nil, "cat-file", "-e", obj)
	return err == nil
}

func (r Repo) IsAncestor(ancestor, descendant string) bool {
	_, err := r.run(nil, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func (r Repo) DirtyFiles() ([]string, error) {
	return r.collectPaths(
		[]string{"diff", "--name-only"},
		[]string{"diff", "--cached", "--name-only"},
		[]string{"ls-files", "--others", "--exclude-standard"},
	)
}

func (r Repo) ChangedFilesSince(base string) ([]string, error) {
	return r.collectPaths(
		[]string{"diff", "--name-only", base, "HEAD"},
		[]string{"diff", "--name-only"},
		[]string{"diff", "--cached", "--name-only"},
		[]string{"ls-files", "--others", "--exclude-standard"},
	)
}

func (r Repo) collectPaths(commands ...[]string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, args := range commands {
		b, err := r.run(nil, args...)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				seen[line] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (r Repo) TrackedFiles() ([]string, error) {
	b, err := r.run(nil, "ls-files")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// PresentFiles returns version-controlled and untracked, non-ignored files
// that currently exist in the implementation workspace. Integrity policies
// must include untracked files: otherwise a new path protected by a glob could
// evade the pre-checkpoint hash until after Git had already committed it.
func (r Repo) PresentFiles() ([]string, error) {
	return r.collectPaths(
		[]string{"ls-files"},
		[]string{"ls-files", "--others", "--exclude-standard"},
	)
}

func (r Repo) Add(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"add", "-A", "--"}
	args = append(args, paths...)
	_, err := r.run(nil, args...)
	return err
}

func (r Repo) Commit(message string) error {
	_, err := r.run(nil, "commit", "-m", message)
	return err
}

// CommitPaths commits only the supplied paths. Checkpoints use this instead of
// committing the whole index so an unrelated pre-staged local-control change
// cannot accidentally become accepted workflow work.
func (r Repo) CommitPaths(message string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := []string{"commit", "--only", "-m", message, "--"}
	args = append(args, paths...)
	_, err := r.run(nil, args...)
	return err
}

func (r Repo) HasStagedChanges() (bool, error) {
	cmd := exec.Command("git", "-C", r.Root, "diff", "--cached", "--quiet")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func (r Repo) HasNetChange(start string) (bool, error) {
	cmd := exec.Command("git", "-C", r.Root, "diff", "--quiet", start, "HEAD")
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	// A phase may have produced a new untracked file. Git's commit-to-commit
	// diff intentionally omits it, but it is still a net repository/workspace
	// change and the checkpoint policy will either stage it or reject it.
	dirty, err := r.DirtyFiles()
	if err != nil {
		return false, err
	}
	return len(dirty) > 0, nil
}

func (r Repo) LogSince(base string) (string, error) {
	b, err := r.run(nil, "--no-pager", "log", "--oneline", base+"..HEAD")
	return string(b), err
}
