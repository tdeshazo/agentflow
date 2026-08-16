# Go interpreter

AgentFlow includes an experimental Go interpreter for the executable core of
`agentflow.dev/v1alpha1`.

The runtime is intentionally conservative: implemented constructs execute, while
unknown preconditions, tools, assertions, or template expressions fail closed.
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
refs/agentflow/<workflow-name>/base
refs/agentflow/<workflow-name>/branch
refs/agentflow/<workflow-name>/active
refs/agentflow/<workflow-name>/integrity
refs/agentflow/<workflow-name>/phases/<phase-id>
refs/agentflow/<workflow-name>/human/<gate-id>
refs/agentflow/<workflow-name>/complete
```

Commit-valued records point directly at repository commits. Structured records
such as the active phase, branch name, and integrity baseline are JSON blobs
written with `git hash-object -w` and referenced through the same namespace.
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
- shell and workspace-policy validation tools;
- one bounded repair attempt;
- automatic Git checkpoints of allowed dirty files;
- resumable active phases and commit-aware phase markers;
- interactive human gates with durable commit evidence;
- conditional flow steps, validation steps, phase lifecycle actions, phases,
  and human gates;
- flow assertions for clean workspace and empty progress; and
- completion assertions, final validation, checkpoint, and complete marker.

The expression evaluator is deliberately small and parsed before execution. It
supports typed literals, a finite list of workflow/state/progress references,
`not`/`and`/`or`, equality and integer comparisons, `default(...)`,
`progress.is_checked(...)`, and bounded validation-log `tail(...)`. It is not a
general-purpose template language: unknown functions, type mismatches, and
unavailable values fail closed.

## Codex adapter

The Codex adapter uses headless `codex exec`. It supports the workflow's `never`
approval policy and fails closed for other approval policies rather than silently
ignoring them. The adapter passes the declared model, reasoning effort, sandbox,
color, and ephemeral execution settings and captures the final message using
`--output-last-message`.

## Current limits

This is an interpreter MVP, not yet a complete implementation of every field in
the descriptive specification. In particular, parallel DAG scheduling, arbitrary
programming-language expressions, provider APIs other than Codex, and custom tool
plugins are future work. Unsupported executable constructs produce an error
rather than being ignored.
