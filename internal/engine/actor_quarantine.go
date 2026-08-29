package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

type actorQuarantineResult struct {
	worktree   *gitstate.ActorWorktree
	commit     string
	moved      bool
	authorized bool
	imported   bool
}

func (e *Engine) reconcileActorQuarantine(pending PendingActorInvocation, agent workflow.Agent) (actorQuarantineResult, error) {
	result := actorQuarantineResult{authorized: true}
	if pending.QuarantinePath == "" || pending.BaselineTree == "" {
		return result, errors.New("pending actor quarantine is incomplete")
	}
	if err := gitstate.ValidateQuarantinePath(e.Repo, pending.QuarantinePath); err != nil {
		return result, fmt.Errorf("validate recovered actor quarantine: %w", err)
	}
	if _, err := os.Stat(pending.QuarantinePath); err != nil {
		var outcome ActorInvocationOutcome
		if os.IsNotExist(err) {
			if ok, readErr := e.Store.GetJSON(e.invocationOutcomeRecord(), &outcome); readErr != nil {
				return result, readErr
			} else if ok && outcome.Actor == pending.Actor && outcome.StartCommit == pending.StartCommit && outcome.QuarantinePath == pending.QuarantinePath && outcome.Imported {
				if cleanupErr := gitstate.CleanupRemovedActorWorktree(e.Repo, pending.QuarantinePath, pending.BaselineTree); cleanupErr != nil {
					return result, fmt.Errorf("finish actor quarantine cleanup: %w", cleanupErr)
				}
				result.commit = outcome.Commit
				result.moved = outcome.HeadMoved
				result.authorized = outcome.Authorized
				result.imported = true
				return result, nil
			}
		}
		return result, fmt.Errorf("inspect actor quarantine %q: %w", pending.QuarantinePath, err)
	}
	worktree, err := gitstate.RecoverActorWorktree(
		e.Repo,
		pending.QuarantinePath,
		pending.StartCommit,
		pending.BaselineTree,
		pending.BaselinePermissions,
		pending.Submodules,
	)
	if err != nil {
		return result, fmt.Errorf("recover actor quarantine: %w", err)
	}
	result.worktree = worktree
	head, err := worktree.Repo.Head()
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, "", pending.QuarantinePath, err)
	}
	result.commit = head
	rootMoved := head != pending.StartCommit
	submoduleMoved, err := worktree.SubmoduleHeadsMoved()
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	result.moved = rootMoved || submoduleMoved
	if result.moved && !e.effectiveActorCommitPermission(agent) {
		result.authorized = false
		return result, e.quarantineSafetyViolation(
			pending.Actor,
			head,
			pending.QuarantinePath,
			fmt.Errorf("repository policy: actor %q moved quarantine HEAD but effective actor commit permission is false (may_commit is false and no workflow actor-commit permission is enabled)", pending.Actor),
		)
	}
	if !worktree.Repo.IsAncestor(pending.StartCommit, head) {
		return result, e.quarantineSafetyViolation(
			pending.Actor,
			head,
			pending.QuarantinePath,
			fmt.Errorf("repository policy: actor %q moved quarantine HEAD outside the invocation lineage", pending.Actor),
		)
	}

	finalTree, err := worktree.FinalTree()
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, fmt.Errorf("snapshot actor changes: %w", err))
	}
	finalPermissions, err := worktree.FilePermissions()
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, fmt.Errorf("snapshot actor permissions: %w", err))
	}
	changed, err := worktree.ChangedPaths(finalTree, finalPermissions)
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, fmt.Errorf("inspect actor changes: %w", err))
	}
	if prohibited, err := e.actorEngineOwnedPath(pending, changed); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	} else if prohibited != "" {
		return result, e.quarantineSafetyViolation(
			pending.Actor,
			head,
			pending.QuarantinePath,
			fmt.Errorf("repository policy: actor changed progress or bookkeeping file %s, which is engine-owned", prohibited),
		)
	}

	policyEngine := *e
	policyEngine.Repo = worktree.Repo
	if err := policyEngine.assertIntegrity(); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	if err := policyEngine.assertActorChangedPathsAllowed(changed); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	if err := policyEngine.assertActorCumulativeScope(); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	primaryHead, err := e.Repo.Head()
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	if primaryHead != pending.StartCommit && (!result.moved || primaryHead != head) {
		return result, e.quarantineSafetyViolation(
			pending.Actor,
			head,
			pending.QuarantinePath,
			fmt.Errorf("repository policy: authoritative HEAD changed during actor %q invocation", pending.Actor),
		)
	}
	patch, err := worktree.Patch(finalTree)
	if err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, fmt.Errorf("build actor import: %w", err))
	}
	if _, err := worktree.ImportSubmoduleChanges(); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	if _, err := e.Repo.ApplyPatchIdempotent(patch, finalPermissions); err != nil {
		return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, err)
	}
	if rootMoved && primaryHead != head {
		if err := e.Repo.AdoptActorHead(head); err != nil {
			return result, e.quarantineSafetyViolation(pending.Actor, head, pending.QuarantinePath, fmt.Errorf("import authorized actor commits: %w", err))
		}
	}
	result.imported = true
	return result, nil
}

func (e *Engine) assertActorChangedPathsAllowed(changed []string) error {
	if _, ok, err := e.Store.Resolve(e.baseRecord()); err != nil {
		return err
	} else if !ok {
		return nil
	}
	allowed := e.Workflow.Spec.Workspace.MutationPolicy.Allowed
	for _, path := range e.filterIgnored(changed) {
		if !matchesAny(allowed, path) {
			return &safetyViolation{err: fmt.Errorf("out-of-scope file changed: %s", path)}
		}
	}
	return nil
}

// assertActorCumulativeScope retains the ordinary since-base policy check for
// the quarantine. The shared policy path recursively expands initialized
// submodule gitlinks, so both actor changes and pre-existing workspace state
// are evaluated at their actual repository-relative paths.
func (e *Engine) assertActorCumulativeScope() error {
	files, err := e.changedImplementationFiles()
	if err != nil {
		return err
	}
	allowed := e.Workflow.Spec.Workspace.MutationPolicy.Allowed
	for _, path := range files {
		if !matchesAny(allowed, path) {
			return &safetyViolation{err: fmt.Errorf("out-of-scope file changed: %s", path)}
		}
	}
	return nil
}

func (e *Engine) actorEngineOwnedPath(pending PendingActorInvocation, changed []string) (string, error) {
	if pending.PhaseID == "" || len(changed) == 0 {
		return "", nil
	}
	phase, err := e.phaseByID(pending.PhaseID)
	if err != nil {
		return "", err
	}
	paths, err := e.engineOwnedProgressFiles(e.context(phase), phase)
	if err != nil {
		return "", err
	}
	owned := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if filepath.IsAbs(path) {
			path, err = filepath.Rel(e.Repo.Root, path)
			if err != nil {
				return "", err
			}
		}
		owned[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}
	for _, path := range changed {
		if _, prohibited := owned[path]; prohibited {
			return path, nil
		}
	}
	return "", nil
}

func (e *Engine) quarantineSafetyViolation(actor, commit, path string, cause error) *safetyViolation {
	violation := &safetyViolation{
		err:        fmt.Errorf("%w; actor changes quarantined at %s", cause, path),
		actor:      actor,
		commit:     commit,
		quarantine: path,
	}
	var policyViolation *safetyViolation
	if errors.As(cause, &policyViolation) {
		violation.integrityViolation = policyViolation.integrityViolation
	}
	return violation
}
