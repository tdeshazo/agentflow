# Go interpreter

AgentFlow includes an experimental Go interpreter for the executable core of
`agentflow.dev/v1alpha1`.

The runtime is intentionally conservative: implemented constructs execute, while
unknown preconditions, tools, assertions, or template expressions fail closed.
The validator rejects a valid-but-unsupported provider, backend, tool, or policy
before a repository is opened.
This prevents descriptive fields from being silently treated as enforcement.

## CLI

```sh
go run ./cmd/agentflow run \
  -f examples/finish-priority-05.agent-workflow.yaml \
  -C /path/to/target/repository
```

State can be inspected or reset independently:

```sh
go run ./cmd/agentflow status -f workflow.yaml -C /path/to/repo
go run ./cmd/agentflow reset  -f workflow.yaml -C /path/to/repo
```

Runtime parameters can be overridden with repeated `--set key=value` flags.
Environment-backed parameter defaults continue to work through the workflow.

## Git-backed state

The interpreter does not maintain a separate state database. It stores durable
workflow evidence in Git's own object database and namespaced refs:

```text
refs/agentflow/workflow-<hex-encoded-workflow-name>/base
refs/agentflow/workflow-<hex-encoded-workflow-name>/branch
refs/agentflow/workflow-<hex-encoded-workflow-name>/active
refs/agentflow/workflow-<hex-encoded-workflow-name>/integrity
refs/agentflow/workflow-<hex-encoded-workflow-name>/run-identity
refs/agentflow/workflow-<hex-encoded-workflow-name>/phases/<phase-id>
refs/agentflow/workflow-<hex-encoded-workflow-name>/human/<gate-id>
refs/agentflow/workflow-<hex-encoded-workflow-name>/complete
```

The names above are the defaults. `spec.state.records` may configure the base,
branch, active-phase, completed-phase, human, completion, and integrity record
names; the interpreter uses those names within the same workflow namespace.

Commit-valued records point directly at repository commits. Structured records
such as the active phase, branch name, and integrity baseline are JSON blobs
written with `git hash-object -w` and referenced through the same namespace.
`run-identity` is a fixed runtime record, separate from the configurable
branch/base/integrity records. At initialization it records SHA-256 digests of
the executable workflow definition, all resolved run parameters, and other
execution inputs such as the selected workspace and directly referenced
environment values. The canonical input bytes and parameter values are never
persisted. On `run`, an existing identity must match before the runtime reuses
active phases,
checkpoints, repair budgets, human evidence, or completion markers. A changed
task, model parameter, environment-backed value, or executable workflow
definition is rejected with a non-secret diagnostic; use `reset` (or a
workflow-controlled reset) to intentionally abandon that history. An exact
restart continues normally. `status` and explicit `reset` only inspect or
discard state, so they do not require repeating the original task or secret
parameters.
An active-phase record also carries `actor_completed`: it becomes true only
after the primary phase provider returns successfully. A green deterministic
gate is not a substitute for that evidence, so recovery reruns an actor whose
completion was never durably recorded and resumes acceptance without replaying
an actor whose completion was recorded.
When deterministic validation fails, the same record stores a typed
`failure_kind` (`validation` or `safety`); repair budgeting applies only to the
former, while repository-policy safety failures remain terminal.
The workflow-name component is a byte-for-byte hex encoding, so two names that
would sanitize to the same Git-ref spelling still retain isolated state.
Deleting/resetting workflow state deletes only these refs; normal repository
history is not rewritten.

These refs are local workflow state unless a user deliberately configures Git to
push them. AgentFlow does not push orchestration refs by default.

This gives the runtime several useful Git properties for free:

- phase evidence is tied to concrete commits;
- completed markers can be invalidated when they are no longer ancestors of
  `HEAD`;
- workflow state survives interpreter process restarts;
- state storage does not dirty the worktree; and
- no external database needs to be synchronized with repository lineage.

`status` derives its state from these durable records. It distinguishes an
`uninitialized` workflow, an initialized `ready` workflow, an `active` phase, a
recoverable `validation-failed/recoverable` phase, terminal
`safety-failed/terminal`, a pending `human-gated` review, and a `completed`
workflow. Stale completion markers are not reported as completed when their
commit is no longer an ancestor of `HEAD`. Status only needs the workspace
location; it does not require the original task, model values, or other run
parameters.

`reset` is an explicit state operation. It removes only this workflow's
namespaced records and preserves repository history and source changes; when
the workflow requires a clean implementation workspace, reset refuses to run
until that workspace is clean. A workflow-controlled reset is the intentional
way to abandon a prior run identity and begin with changed inputs.

## Provider interface

AI execution is provider-neutral. Providers implement:

```go
type Provider interface {
    Name() string
    Run(context.Context, Request) (Result, error)
}
```

`Request` describes workspace, model, reasoning effort, prompt, sandbox, and
execution-lifetime preferences without exposing Codex-specific command-line
arguments to the interpreter.

The initial provider is `codex`. It maps an AgentFlow actor to non-interactive
`codex exec`, passes prompts on stdin, uses the declared model/reasoning/sandbox,
and captures the final message. Workflow acceptance does not depend on the final
message; deterministic validation still owns advancement.

## Executed v1alpha1 core

Before running a workflow, validate its document without opening a repository,
creating Git state, or starting a provider:

```sh
go run ./cmd/agentflow validate -f workflow.yaml
```

Validation reports one of three outcomes: `invalid` for YAML/schema/reference
errors, `valid and executable` for the current runtime surface, and `valid but
unsupported by this runtime` for documented `v1alpha1` constructs that this
bootstrap interpreter intentionally does not execute. `run`, `status`, and
`reset` reject both invalid and unsupported workflows before creating an engine.
Diagnostics include YAML paths and source positions for semantic errors where
the YAML parser exposes them.

The current runtime supports the following executable core:

- typed parameters with deterministic defaults, environment values, and CLI
  overrides;
- a bounded, parsed expression language with typed boolean conditions;
- Git repository, command, file, containment, lineage, branch, and integrity
  preconditions;
- allowed-path mutation policy;
- exact/group/normalized integrity hashing;
- Markdown checklist progress and one-criterion progress invariants;
- first-unchecked progress selection and bounded next-unchecked loops;
- named AI phases with provider-neutral actors;
- shell, workspace-policy, Git-checkpoint, file-regex, and Markdown-checklist
  validation tools;
- captured shell output and bounded validation failure logs;
- one bounded repair attempt;
- automatic Git checkpoints of allowed dirty files;
- resumable active phases and commit-aware phase markers;
- interactive human gates with conditional skip, placement prerequisites, and
  durable configured evidence records;
- conditional flow steps, validation steps, phase lifecycle actions, phases,
  declarative active-phase recovery, and human gates;
- flow assertions for clean workspace and empty progress; and
- completion assertions, final validation, checkpoint, configured completion
  marker, and deterministic summary fields.

The expression evaluator is deliberately small and parsed before execution. It
supports typed literals, a finite list of workflow/state/progress references,
`not`/`and`/`or`, equality and integer comparisons, `default(...)`,
`progress.is_checked(...)`, and bounded validation-log `tail(...)`. It is not a
general-purpose template language: unknown functions, type mismatches, and
unavailable values fail closed.

## Codex adapter

The Codex adapter uses headless `codex exec`. It supports the workflow's `never`
approval policy and fails closed for other approval policies rather than silently
ignoring them. It explicitly passes `-c approval_policy="never"`, which overrides
any user configuration for that process, as well as the declared model, reasoning
effort, sandbox, color, and ephemeral execution settings. It captures the final
message using `--output-last-message`.

## Current limits

This is an interpreter MVP, not yet a complete implementation of every field in
the descriptive specification. The following are explicitly non-executable in
this runtime and are rejected by validation: provider runners other than
`codex`, approval policies other than `never`, state backends other than
`git-dir`, non-Git workspaces, non-Markdown progress sources, non-`on-exit` temp
cleanup, `tool` and `human` phase kinds, and tool types outside the list above.
Parallel DAG scheduling,
arbitrary programming-language expressions, and custom tool plugins remain
future work. Unsupported executable constructs produce an error rather than
being ignored.
