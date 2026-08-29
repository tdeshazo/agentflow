package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

const standaloneFailureRecordPrefix = "validation-failures/"

// cleanupRetainedActorQuarantines releases actor worktrees before reset
// discards the durable metadata needed to validate their cleanup.
func (e *Engine) cleanupRetainedActorQuarantines() error {
	authorities := map[string]PendingActorInvocation{}
	retainedPaths := map[string]string{}

	var pending PendingActorInvocation
	pendingExists, err := e.Store.GetJSON(e.pendingInvocationRecord(), &pending)
	if err != nil {
		return fmt.Errorf("inspect pending actor quarantine before reset: %w", err)
	}
	if pendingExists {
		if err := validateActorQuarantineCleanupAuthority(pending); err != nil {
			return fmt.Errorf("validate pending actor quarantine before reset: %w", err)
		}
		if pending.QuarantinePath != "" {
			authorities[pending.QuarantinePath] = pending
			retainedPaths[pending.QuarantinePath] = e.pendingInvocationRecord()
		}
	}

	terminalPaths, err := e.terminalActorQuarantinePaths()
	if err != nil {
		return err
	}
	for path, record := range terminalPaths {
		retainedPaths[path] = record
	}

	unresolved := make(map[string]string)
	for path, record := range terminalPaths {
		if _, ok := authorities[path]; !ok {
			unresolved[path] = record
		}
	}
	if len(unresolved) != 0 {
		var outcome ActorInvocationOutcome
		outcomeExists, err := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome)
		if err != nil {
			return fmt.Errorf("inspect retained actor quarantine authority before reset: %w", err)
		}
		if !outcomeExists {
			return fmt.Errorf("retained actor quarantine has no invocation cleanup authority")
		}
		if err := validateActorQuarantineCleanupAuthority(outcome.PendingActorInvocation); err != nil {
			return fmt.Errorf("validate retained actor quarantine authority before reset: %w", err)
		}
		if _, ok := unresolved[outcome.QuarantinePath]; ok {
			authorities[outcome.QuarantinePath] = outcome.PendingActorInvocation
			delete(unresolved, outcome.QuarantinePath)
		}
		if len(unresolved) != 0 {
			paths := sortedActorQuarantinePaths(unresolved)
			return fmt.Errorf(
				"retained actor quarantine %q from %s does not match invocation cleanup authority",
				paths[0],
				unresolved[paths[0]],
			)
		}
	}

	for _, path := range sortedActorQuarantinePaths(retainedPaths) {
		authority, ok := authorities[path]
		if !ok {
			return fmt.Errorf("retained actor quarantine %q has no cleanup authority", path)
		}
		if err := e.removeRetainedActorQuarantine(authority); err != nil {
			return fmt.Errorf("clean retained actor quarantine from %s: %w", retainedPaths[path], err)
		}
	}
	return nil
}

func (e *Engine) terminalActorQuarantinePaths() (map[string]string, error) {
	paths := map[string]string{}
	var active ActivePhase
	if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil {
		return nil, fmt.Errorf("inspect active actor quarantine before reset: %w", err)
	} else if ok && active.QuarantinePath != "" {
		paths[active.QuarantinePath] = e.activeRecord()
	}

	var lastFailure gitstate.FailureRecord
	if ok, err := e.Store.GetJSON(e.lastFailureRecord(), &lastFailure); err != nil {
		return nil, fmt.Errorf("inspect failed actor quarantine before reset: %w", err)
	} else if ok && lastFailure.QuarantinePath != "" {
		paths[lastFailure.QuarantinePath] = e.lastFailureRecord()
	}

	names, err := e.Store.Names()
	if err != nil {
		return nil, fmt.Errorf("list actor quarantine records before reset: %w", err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, standaloneFailureRecordPrefix) {
			continue
		}
		var failure validationFailureEvidence
		ok, err := e.Store.GetJSON(name, &failure)
		if err != nil {
			return nil, fmt.Errorf("inspect actor quarantine record %q before reset: %w", name, err)
		}
		if ok && failure.QuarantinePath != "" {
			paths[failure.QuarantinePath] = name
		}
	}
	return paths, nil
}

func validateActorQuarantineCleanupAuthority(authority PendingActorInvocation) error {
	if authority.QuarantinePath == "" {
		if authority.BaselineTree != "" || authority.BaselinePermissions != nil || len(authority.Submodules) != 0 {
			return fmt.Errorf("actor quarantine cleanup authority is incomplete")
		}
		return nil
	}
	if authority.BaselineTree == "" {
		return fmt.Errorf("actor quarantine cleanup authority is incomplete")
	}
	return nil
}

func (e *Engine) removeRetainedActorQuarantine(authority PendingActorInvocation) error {
	if err := gitstate.ValidateQuarantinePath(e.Repo, authority.QuarantinePath); err != nil {
		return fmt.Errorf("validate actor quarantine cleanup path: %w", err)
	}
	if _, err := os.Lstat(authority.QuarantinePath); err != nil {
		if os.IsNotExist(err) {
			if err := gitstate.CleanupRemovedActorWorktree(
				e.Repo,
				authority.QuarantinePath,
				authority.BaselineTree,
			); err != nil {
				return fmt.Errorf("finish actor quarantine cleanup: %w", err)
			}
			return nil
		}
		return fmt.Errorf("inspect actor quarantine cleanup path: %w", err)
	}
	worktree, err := gitstate.RecoverActorWorktree(
		e.Repo,
		authority.QuarantinePath,
		authority.StartCommit,
		authority.BaselineTree,
		authority.BaselinePermissions,
		authority.Submodules,
	)
	if err != nil {
		return fmt.Errorf("recover actor quarantine for cleanup: %w", err)
	}
	if err := worktree.Remove(); err != nil {
		return fmt.Errorf("remove actor quarantine: %w", err)
	}
	return nil
}

func sortedActorQuarantinePaths(paths map[string]string) []string {
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	return ordered
}
