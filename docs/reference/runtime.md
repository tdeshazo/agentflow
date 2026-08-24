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
go run . run \
  -f workflow.yaml \
  -C /path/to/target/repository

go run . run code-styling

go run . run --detach \
  -f workflow.yaml \
  -C /path/to/target/repository
```

Foreground execution is the default. `run --detach` starts the same
AgentFlow executable in a new session and returns after the child process has
started; it does not wait for workflow success. The child receives the same
workflow file, repository, Codex binary, and repeated `--set` values, with
stdin disconnected and `--detach` removed. Inspect a detached run with
`status --all -C /path/to/target/repository` and
`logs --workflow workflow-name -C /path/to/target/repository` (optionally
using `--tail N` or `--follow`).

`-C` selects the repository explicitly. For repository-scoped commands, its
default is the current working directory, which must be a Git repository.
Single-workflow `run`, `status`, `reset`, `validate`, and `plan` commands may
also take one positional workflow name. Names are looked up as regular
`.yaml`/`.yml` files in `<repository>/.agentflow/workflows/` and
`~/.agentflow/workflows/`; repository-local files shadow home files. Use `-f`
for an explicit workflow path. The positional form and `-f` cannot be combined.
When both selectors are omitted in an interactive terminal, AgentFlow presents
a sorted numbered workflow picker and accepts one selection line; redirected
or piped commands instead fail with the selector usage error and never read
stdin.

AgentFlow reads optional defaults from `<repository>/.agentflow/config.toml`
and `~/.agentflow/config.toml`. An explicit `-C` selects the repository config;
otherwise the current working directory is used. Values are merged by field,
and parameter maps are merged by key, with this precedence:

```text
command line > repository config > home config > built-in defaults
```

Configuration is typed and rejects unknown keys. The `workflow` values for
`run`, `status`, `reset`, `validate`, and `plan` are logical names from the
discovery directories; explicit workflow selectors override configured
selectors. `logs.workflow` mirrors the runtime workflow name accepted by
`logs --workflow`. `-C` and `-f` are intentionally command-line-only.

```toml
codex_bin = "codex"

[parameters]
model = "gpt-5"
task = "build the portfolio"

[run]
workflow = "art-portfolio"
detach = false

[status]
workflow = "art-portfolio"
json = false
all = false

[reset]
workflow = "art-portfolio"

[validate]
workflow = "art-portfolio"

[plan]
workflow = "art-portfolio"
expanded = true

[logs]
workflow = "art-portfolio"
tail = 100
follow = false
```

Within one configuration file, `status.workflow` conflicts with
`status.all = true`, and `logs.tail` conflicts with `logs.follow = true`.
A higher-precedence selector or mode replaces its lower-precedence conflict.

State can be inspected or reset independently:

```sh
go run . status -f workflow.yaml -C /path/to/repo
go run . status -f workflow.yaml -C /path/to/repo --json
go run . status --all -C /path/to/repo
go run . status --all -C /path/to/repo --json
go run . logs --workflow workflow-name -C /path/to/repo
go run . logs --workflow workflow-name -C /path/to/repo --tail 100
go run . logs --workflow workflow-name -C /path/to/repo --follow
go run . reset  -f workflow.yaml -C /path/to/repo
go run . plan --expanded -f workflow.yaml
```

Runtime parameters can be overridden with repeated `--set key=value` flags.
Environment-backed parameter defaults continue to work through the workflow.

`plan --expanded` validates the authored document, normalizes concise defaults,
and validates the resulting executable representation without opening a
repository or invoking actors/tools. Its YAML output exposes the resolved
lifecycle and executor defaults, recovery, safety boundaries, each gate's
repair actor and post-repair steps, each phase's resolved actor/reasoning,
validation, mutation/progress flags, bookkeeping transitions, and acceptance
ordering, plus the checkpoint contract, human gates, and completion contract.

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
refs/agentflow/workflow-<hex-encoded-workflow-name>/validation-evidence/<run>/<key>
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
For engine-owned progress and bookkeeping transitions it also records the
pre-transition checklist/file-state digests plus a pending/applied marker
before changing Markdown. A restart can therefore finish only an exact
declared transition from one of the durable intermediate states; it cannot
reinterpret an unrelated checkbox, status, or same-file edit as accepted work.
When deterministic validation fails, the same record stores a typed
`failure_kind` (`validation` or `safety`); repair budgeting applies only to the
former, while repository-policy safety failures remain terminal.
Successful deterministic validations additionally write small, digest-only
records under `validation-evidence/`. Their key covers the validation and
referenced tool definitions, resolved input digests, declared dependency
patterns and file-content identity, acceptance policy, run identity, and phase
acceptance context. A later request reuses one only after the runtime rechecks
lineage, protected integrity, and mutation scope. A stale record therefore
cannot authorize work across a safety failure, failed lineage, changed repair
policy, or changed relevant tree. Evidence is namespaced by workflow and run
identity, so it is not a general-purpose artifact cache and cannot collide with
another workflow or concurrent invocation. Capture logs and failure output are
bounded diagnostics, not evidence payloads.
The workflow-name component is a byte-for-byte hex encoding, so two names that
would sanitize to the same Git-ref spelling still retain isolated state.
Deleting/resetting workflow state deletes only these refs; normal repository
history is not rewritten.

These refs are local workflow state unless a user deliberately configures Git to
push them. AgentFlow does not push orchestration refs by default.

Each run also maintains a fixed `observability/descriptor` JSON record in its
workflow namespace. The versioned descriptor contains the workflow name and
the configured names of the base, branch, active-phase, and completion
records, plus optional workflow-file context and process PID/start metadata.
It is rebuildable and observational only: status projection, recovery,
validation, phase advancement, and completion continue to read the existing
acceptance records. It never stores parameter values, environment values,
prompts, secret values, or run-identity source bytes. A PID is reported as
live only when its durable process-start token can be verified; an active phase
alone is never proof that an AgentFlow process is running.

`status --all` scans these fixed descriptors and returns a stable JSON
collection shaped as `{"schema_version":1,"repo":"...","workflows":[...]}`.
Each workflow item reports its durable state, completion flag, optional active
phase, and safely available base/branch/head context. A malformed namespace is
retained as a `malformed` item with deterministic context so it cannot hide
other workflows. `status -f workflow.yaml` remains the authoritative
definition-aware status view and retains its single-object JSON shape.

Runtime logs are stored as restrictive JSON Lines files below the repository's
Git directory, resolved with `git rev-parse --git-path`; linked worktrees
therefore use the correct Git storage location. Each workflow has an isolated
encoded path. Logs are local runtime data, are not in the worktree, are not
published by ordinary commits or pushes, and are preserved by workflow reset.
They record operational boundaries such as workflow start/end, phase start/end,
provider execution, validation, checkpoint, human-gate, and completion events.
The logger does not persist prompts, parameters, environments, or identity
inputs. Workflow-configured shell capture files are separate from these runtime
logs; captured execution output may contain sensitive content and should remain
local with restrictive permissions. `logs --follow` only watches the log file
and cancellation of the reader does not signal or cancel the workflow process.
`--tail` and `--follow` are separate modes; supplying both is rejected. A
negative `--tail` value is rejected, and `--tail 0` emits no existing lines.

## Runtime-owned phase lifecycle

The concise lifecycle contract is `spec.lifecycle.policy: safe-resume`. A phase
may select its deterministic gate with `phase.validation`; otherwise the
lifecycle's `validation` is used. If a document has neither lifecycle policy
nor legacy phase actions, the runtime still applies the same safe defaults and
requires a deterministic phase validation before acceptance.

For a mutable AI phase, the runtime performs this fixed contract:

1. require a clean implementation boundary and validate branch/base lineage,
   protected integrity, and mutation scope;
2. capture the phase-start commit and progress snapshot, then persist the active
   phase before invoking the actor;
3. persist `actor_completed` immediately after the actor returns successfully;
4. run deterministic validation and applicable progress/net-change assertions;
   for `advanceProgress: true`, verify the actor did not edit progress and then
   let the engine advance exactly the declared criterion;
5. checkpoint accepted allowed work, requiring a clean tree and rechecking
   lineage, integrity, and scope;
6. write the completed-phase commit marker; and
7. clear active state only after the marker is valid.

The optional lifecycle `checkpoint` names an existing checkpoint tool for
workflow-specific semantics. Omitting it uses the runtime Git checkpoint.
Lifecycle safety properties are fixed: an override cannot disable deterministic
acceptance, protected-resource checks, scope checks, lineage checks, or the
clean checkpoint boundary. Legacy `phaseDefaults`, phase `after` actions, and
`recovery.activePhase` remain supported for v1alpha1 compatibility and are
treated as explicit procedural escape hatches; their markers are still subject
to the runtime acceptance contract.

Interrupted recovery is derived from the active record. A valid completed
marker wins over stale active state. If `actor_completed` is true, recovery
repeats deterministic acceptance without replaying the actor. Otherwise the
runtime checks retained partial commits and dirty worktree changes with the
phase gate before rerunning the actor. A passing preflight does not substitute
for actor-completion evidence, and a safety failure is terminal. Recovery never
deletes partial commits or working-tree changes.

The runtime applies the same policy boundary before and after actor/tool work,
at checkpoint and acceptance, and before reusing a completed marker. This keeps
workspace allowlists, protected integrity baselines, Git lineage, and required
cleanliness as runtime invariants rather than repeated assertions an author can
accidentally omit.

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
workflow. An active record without a durable initialization base is reported as
`stale` and carries no recovery instruction. Malformed active records fail
closed rather than producing recovery advice. Stale completion markers are not
reported as completed when their commit is no longer an ancestor of `HEAD`.
Status only needs the workspace location; it does not require the original
task, model values, or other run parameters.

## Recovering from a failed run

When a failed `run` has durable phase state, AgentFlow appends a recovery footer
after the primary error. The normal operator sequence is:

1. Read the primary error and AgentFlow recovery footer.
2. Inspect `status` and `logs`.
3. Make any required deterministic/manual correction, including repairing or
   reverting a workspace-policy violation.
4. Rerun the same `agentflow run` invocation. AgentFlow uses its normal durable
   recovery checks to resume retained phase work when safe.

For a `safety-failed/terminal` state, automatic actor/repair work has stopped
for the current unsafe workspace condition, but the durable run is not
abandoned: restore policy compliance and rerun. `reset` is only for
intentionally abandoning the durable run, and is not required merely because a
safety failure occurred.

Pass `--json` for one machine-readable object instead of the default text. It
contains `schema_version`, `workflow`, `repo`, `state`, `initialized`, and
`complete`, plus available `base`, `branch`, phase, gate, and commit context.
Validation failure output is represented by its non-secret validation name and
failure kind; prompts, reasoning, parameter values, environment values, and
command output are not included. When stdout is an interactive terminal, the
object is indented for readability. Redirected, piped, buffered, and other
non-terminal stdout remains compact JSON; both forms end with one newline.
Actionable failures also include stable `recovery` and `next_action` fields:
validation failures use `automatic-on-rerun`/`rerun`, while safety failures use
`operator-action-required`/`remediate-then-rerun`.
The same presentation rule applies to the collection returned by
`status --all --json`, without changing its schema or workflow ordering.

Human-readable status, validation results, usage text, errors, and detached-run
confirmations use restrained ANSI styling only when their actual output writer
is a terminal. Set `NO_COLOR` to keep the same human-readable text without
styling. Redirected, piped, buffered, and structured output remains free of
presentation escapes; workflow logs, provider streams, and detached capture
are emitted unchanged.

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
go run . validate -f workflow.yaml
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
- engine-owned criterion advancement by stable `criterionID`, with durable
  before-state and exact-delta enforcement;
- actor-less, durable Markdown checklist/status/index bookkeeping transitions
  that preserve non-bookkeeping content byte-for-byte;
- first-unchecked progress selection and bounded next-unchecked loops;
- named AI phases with provider-neutral actors;
- shell, workspace-policy, Git-checkpoint, file-regex, and Markdown-checklist
  validation tools;
- captured shell output and bounded, redacted validation failure logs;
- durable content-addressed success evidence for equivalent deterministic
  validation requests;
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
