package gitstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Discovery owns the complete refs/agentflow/ subtree. Recovery pins live in
// a separate internal namespace so they can never be projected as workflows.
const actorBaselineRefNamespace = "refs/agentflow-internal/quarantines"

type actorBaselinePin struct {
	repo Repo
	ref  string
	tree string
}

// actorBaselineRef returns a ref identity that can be reconstructed from the
// durable quarantine path. Submodule paths are hashed because valid workspace
// paths are not necessarily valid Git ref components.
func actorBaselineRef(worktreePath, submodulePath string) (string, error) {
	parent, err := parseActorQuarantineParent(worktreePath)
	if err != nil {
		return "", err
	}
	quarantineID := filepath.Base(parent)
	component := "root"
	if submodulePath != "" {
		digest := sha256.Sum256([]byte(submodulePath))
		component = "submodule-" + hex.EncodeToString(digest[:16])
	}
	return actorBaselineRefNamespace + "/" + quarantineID + "/" + component, nil
}

func createActorBaselinePin(repo Repo, ref, tree string) error {
	if ref == "" || tree == "" {
		return fmt.Errorf("actor quarantine baseline pin is incomplete")
	}
	if _, err := repo.run(nil, "cat-file", "-e", tree+"^{tree}"); err != nil {
		return fmt.Errorf("inspect actor quarantine baseline tree: %w", err)
	}
	// An empty old value makes creation fail if a ref with this identity
	// already exists; a quarantine must never overwrite another recovery pin.
	if _, err := repo.run(nil, "update-ref", ref, tree, ""); err != nil {
		return fmt.Errorf("pin actor quarantine baseline: %w", err)
	}
	return nil
}

func readActorBaselinePin(repo Repo, ref string) (string, bool, error) {
	output, err := repo.run(nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		// rev-parse exits with status 1 and no output when the ref does not
		// exist. Any diagnostic output is an operational failure instead.
		if len(output) == 0 {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(output)), true, nil
}

func validateActorBaselinePin(pin actorBaselinePin) error {
	got, ok, err := readActorBaselinePin(pin.repo, pin.ref)
	if err != nil {
		return fmt.Errorf("inspect actor quarantine baseline pin %q: %w", pin.ref, err)
	}
	if !ok {
		return fmt.Errorf("actor quarantine baseline pin %q is missing", pin.ref)
	}
	if got != pin.tree {
		return fmt.Errorf("actor quarantine baseline pin %q names %s, want %s", pin.ref, got, pin.tree)
	}
	if _, err := pin.repo.run(nil, "cat-file", "-e", pin.tree+"^{tree}"); err != nil {
		return fmt.Errorf("inspect actor quarantine baseline pinned by %q: %w", pin.ref, err)
	}
	return nil
}

func releaseActorBaselinePin(pin actorBaselinePin, allowMissing bool) error {
	got, ok, err := readActorBaselinePin(pin.repo, pin.ref)
	if err != nil {
		return fmt.Errorf("inspect actor quarantine baseline pin %q: %w", pin.ref, err)
	}
	if !ok {
		if allowMissing {
			return nil
		}
		return fmt.Errorf("actor quarantine baseline pin %q is missing", pin.ref)
	}
	if got != pin.tree {
		return fmt.Errorf("actor quarantine baseline pin %q names %s, want %s", pin.ref, got, pin.tree)
	}
	if _, err := pin.repo.run(nil, "update-ref", "-d", pin.ref, pin.tree); err != nil {
		return fmt.Errorf("release actor quarantine baseline pin %q: %w", pin.ref, err)
	}
	return nil
}

func (w *ActorWorktree) baselinePins() ([]actorBaselinePin, error) {
	pins := make([]actorBaselinePin, 0, len(w.Submodules)+1)
	if w.BaselineTree != "" {
		ref, err := actorBaselineRef(w.Repo.Root, "")
		if err != nil {
			return nil, err
		}
		// The superproject ref is shared with the primary repository and
		// remains accessible after Git removes the linked worktree.
		pins = append(pins, actorBaselinePin{repo: w.Primary, ref: ref, tree: w.BaselineTree})
	}
	for _, snapshot := range w.Submodules {
		repo, err := actorSubmoduleRepo(w.Repo.Root, snapshot.Path)
		if err != nil {
			return nil, fmt.Errorf("resolve actor submodule %q baseline pin: %w", snapshot.Path, err)
		}
		ref, err := actorBaselineRef(w.Repo.Root, snapshot.Path)
		if err != nil {
			return nil, err
		}
		pins = append(pins, actorBaselinePin{repo: repo, ref: ref, tree: snapshot.BaselineTree})
	}
	sort.Slice(pins, func(i, j int) bool { return pins[i].ref < pins[j].ref })
	return pins, nil
}

func (w *ActorWorktree) validateBaselinePins() error {
	pins, err := w.baselinePins()
	if err != nil {
		return err
	}
	if len(pins) == 0 {
		return fmt.Errorf("actor quarantine baseline pin is missing")
	}
	for _, pin := range pins {
		if err := validateActorBaselinePin(pin); err != nil {
			return err
		}
	}
	return nil
}

// CleanupRemovedActorWorktree completes cleanup after a compliant quarantine
// checkout disappeared or Git removed it, but before its durable pending
// invocation was cleared. The imported outcome proves that the baseline is no
// longer recovery authority, so a missing pin is already-cleaned success.
func CleanupRemovedActorWorktree(primary Repo, path, baselineTree string) error {
	parent, err := actorQuarantineParent(primary, path)
	if err != nil {
		return err
	}
	registered, err := actorWorktreeRegistered(primary, path)
	if err != nil {
		return fmt.Errorf("inspect removed actor quarantine registration: %w", err)
	}
	if registered {
		if _, err := primary.run(nil, "worktree", "remove", "--force", path); err != nil {
			return fmt.Errorf("unregister removed actor quarantine worktree: %w", err)
		}
	}
	ref, err := actorBaselineRef(path, "")
	if err != nil {
		return err
	}
	if err := releaseActorBaselinePin(actorBaselinePin{
		repo: primary,
		ref:  ref,
		tree: baselineTree,
	}, true); err != nil {
		return err
	}
	rootPath := filepath.Dir(parent)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open actor quarantine root: %w", err)
	}
	defer root.Close()
	if err := root.Remove(filepath.Base(parent)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove actor quarantine directory: %w", err)
	}
	return nil
}

func actorWorktreeRegistered(primary Repo, path string) (bool, error) {
	output, err := primary.run(nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	for _, field := range strings.Split(string(output), "\x00") {
		const prefix = "worktree "
		if strings.HasPrefix(field, prefix) && strings.TrimPrefix(field, prefix) == path {
			return true, nil
		}
	}
	return false, nil
}
