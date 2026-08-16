# Self-hosting AgentFlow development

`examples/develop-agentflow.agent-workflow.yaml` is the repository-owned
workflow for one bounded AgentFlow development task. It runs through the normal
Go interpreter; the YAML owns mutation policy, validation, one repair attempt,
checkpointing, recovery, human review, and completion.

The workflow permits ordinary implementation, specification, test, and guide
changes in `cmd/`, `internal/`, `provider/`, `spec/`, and `docs/`, plus the
small set of root development files listed in its allowlist. It protects
`ROADMAP.md`, research documents, `scripts/check.sh`, the workflow itself, and
the CI quality workflow. A protected-file edit cannot be checkpointed or
accepted.

## Validate

From the repository root, validate the shipped workflow before starting a run:

```sh
go run ./cmd/agentflow validate \
  -f examples/develop-agentflow.agent-workflow.yaml
```

The canonical deterministic gate also validates every shipped workflow:

```sh
./scripts/check.sh
```

## Run

Start from a clean, named Git branch and supply one bounded task. The task is
passed to the Luna/high implementation phase; it must describe a specific
change, not an open-ended development program.

```sh
go run ./cmd/agentflow run \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C . \
  --set "task=Add focused regression coverage for <bounded behavior>."
```

After implementation, Terra/high independently audits the actual checkout,
diff, tests, and gate results. It does not accept the implementation agent's
completion message as evidence. `scripts/check.sh` is the authoritative quality
gate. If implementation validation fails, Terra/high gets exactly one repair
attempt; audit and final gates do not receive additional repair attempts.

The workflow then pauses at `self-host-review`. Inspect the final repository
state and type exactly `yes` to record durable human approval and allow the
completion marker to be written.

## Status

The interpreter stores state in refs namespaced from `metadata.name`
(`develop-agentflow`), so it is isolated from other AgentFlow runs. `status`
works before any later status-output enhancements and is safe to run at any
time, including before the workflow has started:

```sh
go run ./cmd/agentflow status \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C .
```

It reports whether the run is initialized, its saved base and branch, an active
phase if one was interrupted, and whether completion has been recorded.

## Resume

If the interpreter is interrupted, rerun the same command with the same task:

```sh
go run ./cmd/agentflow run \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C . \
  --set "task=Add focused regression coverage for <bounded behavior>."
```

The workflow checks the saved base/branch lineage, preserves partial commits or
worktree changes for the active phase, validates existing work first, and only
reruns that same phase when necessary. Completed phase markers remain valid
only while their commits are ancestors of `HEAD`.

## Reset

Reset discards only this workflow's orchestration refs; it never rewrites normal
Git history or cleans up source changes. It requires a clean implementation
workspace:

```sh
go run ./cmd/agentflow reset \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C .
```

Alternatively, request a reset as part of the next run with
`--set reset_workflow_state=true`. Use reset only when intentionally abandoning
that run's recovery history.

This document describes the vehicle and its checks. It does not claim that a
subsequent AgentFlow change has already been completed through the workflow.
