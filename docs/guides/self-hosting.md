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
go run . validate \
  -f examples/develop-agentflow.agent-workflow.yaml
```

The canonical deterministic Bash gate also validates every shipped workflow:

```sh
./scripts/check.sh
```

The gate is the repository-owned quality authority and never makes a live model
call. CI invokes this executable directly, and the self-hosting workflow uses
the same command.

## Run

Start from a clean, named Git branch and supply one bounded task. The task is
passed to the Luna/high implementation phase; it must describe a specific
change, not an open-ended development program.

```sh
go run . run \
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
go run . status \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C .
```

Automation can request the same durable view as one JSON object:

```sh
go run . status --json \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C .
```

It reports a durable state such as `uninitialized`, `ready`, `active`,
`validation-failed/recoverable`, `safety-failed/terminal`, `human-gated`, or
`completed`, along with the saved base/branch, active phase, failed validation,
commit, and human-gate context when present. The JSON form keeps this context
machine-readable and excludes prompts, reasoning, task/secret parameters,
environment values, and command output. Status does not require the original
task or model parameters.

## Resume

If the interpreter is interrupted, rerun the same command with the exact same
task, model/runtime inputs, and executable workflow definition:

```sh
go run . run \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C . \
  --set "task=Add focused regression coverage for <bounded behavior>."
```

The workflow checks the saved base/branch lineage, preserves partial commits or
worktree changes for the active phase, validates existing work first, and only
reruns that same phase when necessary. If an implementation or audit actor was
interrupted before returning, its phase cannot be accepted from a passing gate;
the actor is resumed. If the actor already returned successfully but acceptance
was interrupted, deterministic acceptance resumes without replaying it.
Completed phase markers remain valid only while their commits are ancestors of
`HEAD`.

## Reset

Reset discards only this workflow's orchestration refs; it never rewrites normal
Git history or cleans up source changes. It requires a clean implementation
workspace, so partial work must be handled or committed before resetting:

```sh
go run . reset \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C .
```

Alternatively, request a reset as part of the next run with
`--set reset_workflow_state=true`. Use reset only when intentionally abandoning
that run's recovery history.

This document describes the vehicle and its checks. See the [self-hosting MVP
evidence](../evidence/self-hosting-mvp.md) for the retained proof of a completed
run.
