package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// authoritativeWorkspaceSnapshot covers HEAD and every non-Git worktree entry,
// including untracked and ignored files. MutationNone is stronger than normal
// workflow allowlists: no repository delta is permitted during the call.
func (e *Engine) authoritativeWorkspaceSnapshot() (string, error) {
	head, err := e.Repo.Head()
	if err != nil {
		return "", err
	}
	entries := []string{"HEAD\x00" + head}
	err = filepath.WalkDir(e.Repo.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(e.Repo.Root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		identity := filepath.ToSlash(rel) + "\x00" + info.Mode().String() + "\x00"
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			identity += target
		case info.Mode().IsRegular():
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(contents)
			identity += hex.EncodeToString(digest[:])
		}
		entries = append(entries, identity)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("snapshot authoritative workspace: %w", err)
	}
	sort.Strings(entries[1:])
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(digest[:]), nil
}
