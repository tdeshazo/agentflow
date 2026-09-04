# Go interpreter

AgentFlow includes an experimental Go interpreter for `agentflow.dev/v1alpha1`
and the successor contracts through `agentflow.dev/v1alpha4`.

The runtime is intentionally conservative: implemented constructs execute, while
unknown preconditions, tools, assertions, or template expressions fail closed.
The validator rejects a valid-but-unsupported provider, backend, tool, or policy
before a repository is opened.
This prevents descriptive fields from being silently treated as enforcement.

## Fresh context and advisory handoffs

A provider that negotiates `agentflow.dev/provider/v2` may receive
`agentflow.dev/invocation-context/v2`. The engine deterministically compiles
the current objective, authority, workspace state, direct accepted
dependencies, declared artifacts/evidence, accepted direct handoffs, and
validations under a non-configurable 65,536-byte ceiling. Its receipt explains
selected opaque IDs and omissions without exposing omitted payloads.

Audit and typed-output phases require `provider/v2`,
`invocation-context/v2`, and native `agentflow.dev/handoff/v1` output at
preflight. Ordinary phases alone may use the v1 compatibility path. The engine
validates bounded, schema-shaped, non-secret output and
publishes it only after deterministic acceptance, bound to the run, node,
phase, and commit. Claims remain advisory; only deterministic validations and
the phase marker authorize later work.

## CLI

```sh
go run . run \
  -f workflow.yaml \
  -C /path/to/target/repository

go run . run code-styling

go run . run --detach \
  -f workflow.yaml \
  -C /path/to/target/repository

go run . attach \
  -f workflow.yaml \
  -C /path/to/target/repository

go run . explain --node build \
  -f workflow.yaml \
  -C /path/to/target/repository
```

Foreground execution is the default. In an interactive terminal, ordinary
`run` starts the supervised child and immediately becomes its exclusive
authenticated foreground attachment; non-interactive callers retain direct
execution semantics. `run --detach` starts the same
supervised child without attaching and returns only after the child has
acquired the run lease, established its durable run identity, and replied over
a bounded private readiness pipe; it does not wait for workflow success. A
startup error or readiness timeout is returned instead of being hidden after
an optimistic success message. The child receives the same
workflow file, repository, Codex binary, and repeated `--set` values, with
stdin disconnected and `--detach` removed. The detached child creates a
runtime-private supervised session only after it has a durable run ID. Its
readiness reply also reports whether verified attachment is available on this
host. For an interactive foreground launch the child then waits at this
boundary until the parent has authenticated and reserved the attachment and
acknowledged it over the private return pipe. No actor can run in that window.
Detached launch does not wait for a terminal acknowledgement. Unix passes the
two pipe ends as inherited descriptors; Windows uses its explicit inherited
handle list rather than unsupported `Cmd.ExtraFiles`. Inspect a
detached run with `status --all -C /path/to/target/repository` and
`logs --workflow workflow-name -C /path/to/target/repository` (optionally
using `--tail N` or `--follow`).

`attach` is the detached-to-foreground handoff. It requires the same workflow
definition and repository, verifies the durable run ID, the descriptor's
process-start token, and a restrictive private session record before opening a
local IPC endpoint. The live supervisor owns a per-run, 1 MiB rotating replay
window and replays at most 200 output chunks by default (`--replay 0` disables
replay), then streams later output without a handoff gap. Replay from an older
run is never eligible. Only one terminal may be attached. Sending EOF (usually
Ctrl-D) is the explicit foreground-to-detached handoff: it releases terminal
ownership while the same run ID, lease, process, and in-flight work continue.
The foreground command returns only after the supervisor acknowledges that it
has reclaimed terminal ownership.
`SIGINT` and `SIGTERM` forward the corresponding constrained interruption
request; attach continues draining output until the supervisor sends its final
cursor and completion frame. Attached input is accepted only while an active
human gate is waiting for an acknowledgement or checklist response; it is not
queued for later lifecycle steps. Attachment is deliberately distinct from
`logs --follow`, which is read-only and never signals or supplies input to a
workflow. The separate private diagnostic log is capped at 1 MiB; after that
cap, workflow execution and live terminal delivery continue even though new
diagnostic events are not retained. Private log directories and files must be
owner-only regular filesystem objects; symlink substitution is rejected, with
directory-relative no-follow descriptor opens and owner checks on supported
Unix hosts. Platforms where AgentFlow cannot prove both properties fail closed
instead of using an `Lstat`-then-open fallback.

If the host forbids the private local IPC primitive (for example, a restricted
sandbox), the detached workflow remains protected by its durable owner lease
but is not attachable. AgentFlow does not weaken identity checks or substitute
a public socket. The readiness reply reports this fallback, and `attach`
reports that no live supervised session exists.

`explain --node <phase-id>` reports a stable JSON explanation for a phase. It
uses Git-backed active, accepted, and dependency markers to classify the node;
the versioned trace can supply only a bounded known-safe skip reason and never
overrides durable evidence. It does not render prompts, validation output,
provider output, credentials, or model reasoning. Before explaining existing
state, it compares the authored workflow-definition digest to the durable run
identity without resolving parameter or environment secrets; definition drift
is reported instead of producing a confident explanation. Provider failures
retain only bounded actor/provider identity, failure kind, and stage.

`-C` selects the repository explicitly. For repository-scoped commands, its
default is the current working directory, which must be a Git repository.
Single-workflow `run`, `status`, `reset`, `validate`, `plan`, and `migrate --check` commands may
also take one positional workflow name. `logs` accepts the same logical name
through `--workflow`. Names are looked up as regular
`.yaml`/`.yml` files in `<repository>/.agentflow/workflows/` and
`~/.agentflow/workflows/`; repository-local files shadow home files. Use `-f`
for an explicit workflow path. The positional form and `-f` cannot be combined.
When both selectors are omitted in an interactive terminal, AgentFlow presents
a sorted numbered workflow picker and accepts one selection line; redirected
or piped commands instead fail with the selector usage error and never read
stdin.

`migrate --check -f workflow.yaml` is source-only: it validates an
`agentflow.dev/v1alpha1` document and reports its deterministic migration
classification without opening a repository, invoking an actor, or changing
the YAML. `migrate` has no automatic rewrite mode.

`agentflow switch <workflow-name>` sets a repository/worktree-local active
selector for later selector-less `run`, `status`, `reset`, `validate`, `plan`,
and `logs` commands. `agentflow checkout ...` is an exact compatibility alias.
With no name, `switch` opens the same sorted picker used by selector-less
workflow commands when connected to a terminal; without a terminal it reports
the normal selector-required error without reading stdin. `agentflow switch -`
swaps back to the previous selector, and `agentflow switch --clear` removes
both current and previous selection.

`agentflow current` writes only the stored current logical name, and writes
nothing with success when there is none. `agentflow workflows` writes the
discovered logical names in deterministic order, marking the active one with
`*`, like a branch list. The selection records logical discovery names only.
Commands that need to resolve the selected workflow reject a removed or
otherwise unavailable name as stale, with guidance to switch to a discovered
workflow or clear the selection, rather than silently selecting another
workflow. `current` intentionally reports the stored name even when stale so a
shell script can inspect and clear it.

AgentFlow reads optional defaults from `<repository>/.agentflow/config.toml`
and `~/.agentflow/config.toml`. An explicit `-C` selects the repository config;
otherwise the current working directory is used. Values are merged by field,
and parameter maps are merged by key, with this precedence:

```text
command line > repository config > home config > built-in defaults
```

For workflow selectors only, an active selection is consulted after those
explicit and configured values and before the interactive picker or missing-
selector error. `logs --workflow` keeps its explicit-selector precedence.

Configuration is typed and rejects unknown keys. The `workflow` values for
`run`, `status`, `reset`, `validate`, `plan`, and `logs` are logical names from
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
detail = false

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
Detailed status is definition-aware and cannot be combined with `status.all`.

State can be inspected or reset independently:

```sh
go run . status -f workflow.yaml -C /path/to/repo
go run . status -f workflow.yaml -C /path/to/repo --json
go run . status -f workflow.yaml -C /path/to/repo --detail
go run . status -f workflow.yaml -C /path/to/repo --detail --json
go run . status --all -C /path/to/repo
go run . status --all -C /path/to/repo --json
go run . logs --workflow workflow-name -C /path/to/repo
go run . logs --workflow workflow-name -C /path/to/repo --tail 100
go run . logs --workflow workflow-name -C /path/to/repo --follow
go run . reset  -f workflow.yaml -C /path/to/repo
go run . plan --expanded -f workflow.yaml
go run . switch code-styling -C /path/to/repo
go run . switch -C /path/to/repo
go run . switch - -C /path/to/repo
go run . checkout code-styling -C /path/to/repo
go run . current -C /path/to/repo
go run . workflows -C /path/to/repo
go run . logs -C /path/to/repo
```

Runtime parameters can be overridden with repeated `--set key=value` flags.
Environment-backed parameter defaults continue to work through the workflow.

`plan --expanded` validates the authored document, normalizes concise defaults
or successor authoring forms, and rejects invalid or unsupported runtime surfaces before
building the plan; it does not open a repository or invoke actors/tools. Its YAML output exposes
the resolved lifecycle and executor defaults, recovery, safety boundaries
(including per-invocation `may_commit` enforcement after provider errors),
each gate's repair actor and bounded rerun contract, each phase's resolved
actor/reasoning, validation, mutation/progress flags, bookkeeping transitions,
and acceptance ordering. For v1alpha2 it also exposes the normalized
dependency graph, including the durable acceptance condition on each edge,
plus the checkpoint contract, human gates, and completion contract. A
checkpoint commit is explicitly reported as runtime-owned; it does not consume
or imply an actor's `may_commit` authority.

## Git-backed state

The active CLI workflow selector is intentionally separate from the
Git-backed execution records below. It is a small JSON file under Git's
worktree-specific metadata path (resolved with `git rev-parse --git-path`),
not an AgentFlow ref or workflow evidence. Selection is therefore scoped to
the repository worktree named by `-C` (or the current worktree), while
repository-local workflow discovery still shadows the shared home scope. This
keeps selection out of the implementation workspace and gives linked
worktrees independent selections. It never initializes, resumes, accepts, or
resets workflow execution state.

The interpreter does not maintain a separate state database. It stores durable
workflow evidence in Git's own object database and namespaced refs:

```text
refs/agentflow/workflow-<hex-encoded-workflow-name>/base
refs/agentflow/workflow-<hex-encoded-workflow-name>/branch
refs/agentflow/workflow-<hex-encoded-workflow-name>/active
refs/agentflow/workflow-<hex-encoded-workflow-name>/integrity
refs/agentflow/workflow-<hex-encoded-workflow-name>/run-identity
refs/agentflow/workflow-<hex-encoded-workflow-name>/owner
refs/agentflow/workflow-<hex-encoded-workflow-name>/pending-invocation
refs/agentflow/workflow-<hex-encoded-workflow-name>/phases/<phase-id>
refs/agentflow/workflow-<hex-encoded-workflow-name>/human/<gate-id>
refs/agentflow/workflow-<hex-encoded-workflow-name>/complete
refs/agentflow/workflow-<hex-encoded-workflow-name>/validation-evidence/<run>/<key>
```

The names above are the defaults. `spec.state.records` may configure the base,
branch, active-phase, completed-phase, human, completion, and integrity record
names; the pending-invocation record is runtime-owned and remains in the same
workflow namespace.

Commit-valued records point directly at repository commits. Structured records
such as the active phase, branch name, and integrity baseline are JSON blobs
written with `git hash-object -w` and referenced through the same namespace.
`run-identity` is a fixed runtime record, separate from the configurable
branch/base/integrity records. At initialization it records SHA-256 digests of
the executable workflow definition, all resolved run parameters, and other
execution inputs such as the selected workspace and directly referenced
environment values. Integrity baselines retain each rule's aggregate digest
and content-free repository-relative path digests; file contents are never
stored. Aggregate-only integrity records written by older runtimes remain
readable. The canonical input bytes and parameter values are never persisted.
On `run`, an existing identity must match before the runtime reuses
active phases,
checkpoints, repair budgets, human evidence, or completion markers. A changed
task, model parameter, environment-backed value, or executable workflow
definition is rejected with a non-secret diagnostic; use `reset` (or a
workflow-controlled reset) to intentionally abandon that history. An exact
restart continues normally. `status` and explicit `reset` only inspect or
discard state, so they do not require repeating the original task or secret
parameters.

Before a workflow can initialize, resume, advance, or reset durable state, the
runtime atomically claims the fixed `owner` record. The lease binds the owner
to a PID and kernel process-start token, so a second live process is rejected
and PID reuse cannot be mistaken for the prior owner. A later invocation may
replace the record only after it verifies that the recorded PID/start-token
pair is no longer live; unavailable or malformed identity metadata fails
closed. Lease release is compare-and-delete, so a finishing process cannot
erase a replacement owner. The owner record is runtime-private and survives an
in-run reset until its holder exits.
An active-phase record also carries `actor_completed`: it becomes true only
after the primary phase provider returns successfully. A green deterministic
gate is not a substitute for that evidence, so recovery reruns an actor whose
completion was never durably recorded and resumes acceptance without replaying
an actor whose completion was recorded.
Before any actor provider call, AgentFlow also persists a pending-invocation
record containing the actor name, invocation-start `HEAD`, invocation
role/context, applicable phase ID, and validation identity/scope when required
for repair or completion. This record is execution attribution only. Prompts,
model output, provider final messages, parameter values, environment values,
and secrets are not persisted.
For engine-owned progress and bookkeeping transitions it also records the
pre-transition checklist/file-state digests plus a pending/applied marker
before changing Markdown. A restart can therefore finish only an exact
declared transition from one of the durable intermediate states; it cannot
reinterpret an unrelated checkbox, status, or same-file edit as accepted work.
When deterministic validation fails, the same record stores a typed
`failure_kind` (`validation` or `safety`); repair budgeting applies only to the
former, while repository-policy safety failures remain terminal. Standalone or
final validation uses the corresponding durable validation-failure record and
the same classification.

The shared `Completion.FinalValidation` durability scope is now explicit for
v1alpha1 `spec.completion.<name>.finalValidation` as well as v1alpha2
`completion.validation`: it is
`completion/<completion-name>/<validation-name>`. This is the logical scope
used when deriving the validation-evidence,
validation-failure, repair-budget, and pending repair-invocation identities;
it is not interchangeable with an ordinary standalone validation, a phase
validation, or another completion using the same name. In v1alpha2 the
concise `completion.validation` field uses the normalized default completion
name.

For either API version, when a completion final gate has one repair attempt,
repair-budget consumption is written before the repair provider starts. The deterministic final gate is
run again after repair, but the repair actor's result or commit never supplies
completion evidence. If that rerun passes, the consumed budget remains until
the completion marker is durable; only then is the transient budget eligible
for cleanup. A restart after successful rerun therefore either continues to
completion when the gate still passes or reports exhausted repair budget when
it fails. Completion still requires its assertions, checkpoint and
post-checkpoint checks, scope, lineage, integrity, cleanliness, and durable
complete marker.

Completion-scoped safety state is terminal and survives repair-state
migration, `HEAD` changes, later validation success, and unscoped records. A
pre-upgrade v1alpha1 unscoped validation-name record is conservatively
recognized for a matching completion final gate before repair availability is
evaluated. Its repair budget is migrated to the scoped identity without
lowering attempts; terminal safety remains authoritative in the legacy record
rather than being copied or cleared. Safety wins over non-safety state;
malformed or conflicting legacy state fails closed; migration does not delete
the legacy source. New ordinary v1alpha1 `flow.validate` behavior is outside
this completion contract.

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

1. reconcile any pending actor invocation before replay, validation, acceptance,
   dependency scheduling, or completion;
2. reject a persisted terminal safety failure before ordinary execution;
3. require a clean implementation boundary and validate branch/base lineage,
   protected integrity, and mutation scope;
4. capture the phase-start commit and progress snapshot, then persist the active
   phase before invoking the actor;
5. compile a versioned provider-neutral invocation context containing the
   expanded objective, relevant workspace state, accepted direct dependencies,
   declared typed inputs, effective write/protected/runtime-owned/commit
   authority, executor capabilities, and selected validation gates;
6. persist the pending invocation before `provider.Run`, inspect `HEAD` after it
   returns even on provider error, attribute any movement to that actor, enforce
   the effective commit rule, and durably record the result before clearing the
   pending record;
7. persist `actor_completed` immediately after the actor returns successfully;
8. run deterministic validation and applicable progress/net-change assertions;
   for `advanceProgress: true`, verify the actor did not edit progress and then
   let the engine advance exactly the declared criterion;
9. checkpoint accepted allowed work, requiring a clean tree and rechecking
   lineage, integrity, and scope;
10. write the completed-phase commit marker; and
11. clear active state only after the marker is valid.

The optional lifecycle `checkpoint` names an existing checkpoint tool for
workflow-specific semantics. Omitting it uses the runtime Git checkpoint.
Lifecycle safety properties are fixed: an override cannot disable deterministic
acceptance, protected-resource checks, scope checks, lineage checks, or the
clean checkpoint boundary. Legacy `phaseDefaults` and phase `after` actions
remain generalized v1alpha1 lifecycle declarations and are subject to the same
acceptance contract. `recovery.activePhase` is different: the runtime has never
executed an authored recovery sequence, so it is explicitly non-executable and
validation rejects it as unsupported before an engine is constructed. Recovery
is always derived from durable state and the selected lifecycle policy.

After the stored run identity matches, interrupted recovery first reconciles the
pending invocation record. If the record's start commit differs from current
`HEAD`, the movement is attributed to its persisted actor and checked against
that actor invocation's effective commit permission. This reconciliation
happens before any actor replay, deterministic validation, phase acceptance,
dependency scheduling, or completion. The pending record is removed only after
the attribution outcome and any terminal safety evidence are durable, so a
restart at either side of cleanup repeats idempotent reconciliation without
replaying an actor or consuming another repair budget. A run-identity mismatch
stops before pending state can authorize recovery. A valid completed marker wins
over stale active state. If `actor_completed` is true, recovery repeats
deterministic acceptance without replaying the actor.
Otherwise the runtime checks retained partial commits and dirty worktree changes
with the phase gate before rerunning the actor. A passing preflight does not
substitute for actor-completion or pending-invocation evidence, and a safety
failure is terminal. Recovery never deletes partial commits or working-tree
changes.

The conformance suite exercises the provider boundary with deterministic fake
providers and explicit interruption seams. It covers interruption after the
pending record is written, after an actor commit but before the fake provider
returns, after provider return but before authority reconciliation, and after
authority reconciliation but before pending-record cleanup. In every window,
restart has one outcome: attribute the commit to the persisted actor, accept it
only after successful deterministic revalidation when authorized, or persist
terminal safety when unauthorized. Actor-created commits never satisfy phase
acceptance, `dependsOn`, or final completion by themselves. Runtime-owned
checkpoint commits remain separately authorized for allowed dirty work even
when the current actor has `may_commit: false`.

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

For a `safety-failed/terminal` state, the durable run cannot continue ordinary
execution. Restoring policy compliance, moving or reverting `HEAD`, cleaning
the workspace, or obtaining a later passing validation does not clear the
persisted safety decision. The explicit `reset`/abandon mechanism is required
to discard that terminal state and begin a new run.

Pass `--json` for one machine-readable object instead of the default text. It
contains `schema_version`, `workflow`, `repo`, `state`, `initialized`, and
`complete`, plus available `base`, `branch`, phase, gate, and commit context.
Initialized runs also expose an opaque `run_id`, `trace_schema_version`, and
`trace_path`. An active phase exposes its durable `node_execution_id` and
`node_attempt`; process recovery retains those values, while a genuinely new
attempt receives new identity.
Validation failure output is represented by its non-secret validation name and
failure kind; prompts, reasoning, parameter values, environment values, and
command output are not included. When stdout is an interactive terminal, the
object is indented for readability. Redirected, piped, buffered, and other
non-terminal stdout remains compact JSON; both forms end with one newline.
Actionable failures also include stable `recovery` and `next_action` fields:
validation failures use `automatic-on-rerun`/`rerun`, while safety failures use
`operator-action-required`/`reset-or-abandon`.
When a path-manifest-backed integrity rule fails, status also includes the
content-free `integrity_rule`, `changed`, `added`, and `removed` fields. These
lists contain bounded, safe repository-relative paths and are available in
both single-workflow and repository-wide text and JSON status.
The same presentation rule applies to the collection returned by
`status --all --json`, without changing its schema or workflow ordering.

Pass `--detail` for a bounded, validated view of the run's orchestration trace.
The human form appends a `detail` section with quoted event metadata. With
`--json`, the unchanged top-level status summary gains a `detail` object with
`trace_state`, `event_count`, `event_limit`, `events_truncated`, and
`recent_events`. At most 20 completed events are returned in chronological
order. `trace_state` is one of `not_initialized`, `unavailable`, `missing`,
`available`, or `error`; `trace_error` is present only for an unreadable or
invalid trace. Because traces are diagnostic rather than authoritative, a
missing or invalid trace does not suppress the durable status summary.
`--detail` and `--all` are mutually exclusive so repository-wide status remains
a lightweight, stable inventory.

## Execution traces

AgentFlow writes a run-specific execution trace separately from the workflow
definition, operational logs, and Git acceptance records. Trace schema v1 is an
append-only JSON Lines stream at `.git/agentflow/traces/<run_id>.jsonl`. Every
event contains `schema_version`, a monotonic `sequence`, a UTC `time`, `run_id`,
and `event`. Node-scoped events also contain `node_id`, `node_execution_id`, and
`attempt`; `fields` contains only runtime-allowlisted orchestration metadata.

The trace intentionally excludes prompts, resolved parameters, environment and
credential values, provider output, command output, and private model reasoning.
On resume, AgentFlow validates the existing schema, run binding, and sequence
continuity before appending. The trace is diagnostic evidence and never grants
phase acceptance, dependency release, recovery authority, or completion.

Schema v1 uses the following orchestration vocabulary:

- Run events: `run_started` and `run_finished`.
- Attempt events: `node_attempt_started`, `node_attempt_resumed`,
  `node_attempt_blocked`, `node_attempt_finished`, and `node_skipped`.
- Durable node transitions: `node_state_transition` records actor completion,
  engine-owned progress or bookkeeping application, and work-item publication.
- Provider events: `provider_start` and `provider_end` bracket the full runtime
  boundary. `provider_request` records the adapter, actor/role, context version,
  enforced sandbox/network/approval shape, capability/credential/filesystem-rule
  counts, capture/presentation flags, and applicable hard budgets.
  `provider_response` records duration, input/output token and cost metering,
  final-message presence, and a success/error/cancellation/deadline outcome.
  Static model names are represented only by a domain-separated opaque digest;
  parameter- or environment-expanded model values are labeled `dynamic` and
  are neither stored nor hashed. Reasoning configuration and model reasoning
  are not trace metadata. `actor_invocation_reconciled` records whether
  quarantined authority was imported, observed without import, or rejected.
- Validation events: `validation_start`, `validation_end`,
  `validation_reused`, `validation_failed`, and `validation_repaired`.
- Repair events: `repair_attempt_start`, `repair_attempt_end`, and
  `repair_budget_exhausted`. Attempts and configured maxima are numeric strings
  in `fields`; failure output is never included.
- Tool and checkpoint events: `tool_start`, `tool_end`, `tool_skipped`,
  `checkpoint_start`, and `checkpoint_end`. Tool metadata contains the authored
  tool name/type, declared workspace-mutation and capture flags, duration,
  outcome, and a shell exit code when available. Commands, expanded `with`
  values, paths, regexes, and output are excluded. A successful checkpoint end
  carries the resulting Git commit and whether that checkpoint created a commit.
- Human events: `human_gate_start`, `human_gate_end`, and
  `human_gate_evidence`. Evidence identifies the confirmed, skipped, or reused
  decision, Git-backed record, and commit.
- Acceptance events: `phase_accepted`, `completion_start`, `completion_end`,
  and `completion_evidence`. Evidence events identify the exact Git-backed
  record and commit, including reconciled or reused evidence after interruption.

Every node-scoped event for one attempt carries the same `node_execution_id`
and `attempt`, including parallel siblings and recovery. A blocked attempt is
not a completed attempt: rerunning resumes the same identity until acceptance,
an explicit skip, or reset ends it. Evidence-bearing events are appended only
after their corresponding Git record has been written; recovery emits a
`reconciled` or `reused` event when it finds durable evidence whose original
post-write trace append may have been interrupted.

Provider and tool metadata is runtime-observed and bounded to an explicit field
allowlist. The trace never requests or accepts chain-of-thought, hidden model
reasoning, prompts, objectives, provider final-message content, provider or
command output, credential names or values, resolved parameters, or environment
values. Metering comes from the provider's structured `Usage` result and remains
diagnostic; deterministic validation and Git evidence continue to own acceptance.

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

Stage 7 adds the versioned `provider.ContractProvider` extension. Every actor
provider must implement and positively report the Stage 2 enforcement
interfaces; the versioned contract is additionally required when a successor
workflow declares non-zero provider requirements.
`agentflow.dev/provider/v1` describes portable execution modes (`agent`,
`local-command`, `remote-service`, `human`, or `nested-workflow`), supported
invocation-context versions, and whether filesystem-boundary and execution-
policy enforcement are provided. Successor workflows may declare `agent.requirements`
with those semantic requirements; they never name adapter flags. Explicit
requirements are checked when the engine is constructed and again before a run
starts, before durable state, a worktree, provider call, tool invocation, or
workspace mutation. When present, contract claims are cross-checked against the
mandatory filesystem and execution-policy enforcement interfaces. Legacy
providers remain selectable when they implement those interfaces and the
workflow declares no versioned requirements.

The reference adapters prove the common contract suite with the isolated Codex
adapter and an explicit local-command adapter. The latter deliberately reports
no isolation or policy enforcement, so the actor boundary rejects it instead
of treating host-process execution as a sandbox. Human and nested-workflow
execution modes are modeled for negotiation but are not advertised by either
adapter until an implementation can enforce their required boundaries.

`Request` describes workspace, model, reasoning effort, structured context, sandbox,
execution-lifetime preferences, and an engine-owned filesystem boundary without
exposing Codex-specific command-line arguments to the interpreter. Providers
must enforce every `FilesystemBoundary` rule or reject the request; treating the
rules as prompt advice is unsafe. The authored `Agent.Sandbox` value is copied
to the provider-neutral `Request.Sandbox` value; an empty value is not defaulted
by the engine. The shared runtime evaluates actor-created commit permission as:

```text
agent.MayCommit
OR spec.workspace.agent_commits.allowed
OR spec.workspace.checkpointing.agent_commits_allowed
```

The first source is the current invocation's `Agent.MayCommit`; the latter two
are workflow-level authorities. The rule is evaluated per named actor
invocation, not per phase, and providers must not treat a different actor's
permission as authority for the current invocation. Runtime-owned checkpoint
commits are outside this actor-created commit rule.

The initial provider is `codex`. It maps an AgentFlow actor to non-interactive
`codex exec`, validates the context version, substitutes only the quarantine
workspace placeholder, renders the structured context canonically on stdin,
uses the declared model/reasoning/sandbox,
and honors `output_last_message` as capture intent. When true, the provider is
asked to capture and return its final message when supported; when false, the
runtime does not request that capture. A returned final message is diagnostic
or presentation output only. Workflow acceptance does not depend on it;
deterministic validation still owns advancement, and the message is never
`actor_completed` evidence, dependency evidence, or completion authority.

### Invocation context compilation

Immediately before every primary, resumed, or validation-repair provider call,
the engine first derives a provider-independent semantic context from normalized
workflow authority and current runtime state. After provider-contract
negotiation, a narrow adapter projects that compiled result into
`provider.InvocationContext`. The context includes only the invocation
identity and expanded objective; relevant changed and dirty paths; accepted
direct dependency commits; verified references to declared artifact files and
deterministic evidence; effective write, integrity, read-only, runtime-owned,
and commit authority; executor capabilities; selected validations; and, for a
repair, the selected durable bounded/redacted validation failure.

Artifact bodies, transcripts, provider output, unrelated contracts, broad run
history, timestamps, random quarantine paths, complete environments, and secret
values are excluded. Artifact references carry workspace path, digest, and mode,
so an actor reads the verified file from its quarantine rather than receiving a
copied body. Compilation uses `{{ agentflow.workspace }}` as a stable workspace
identity. Only the provider adapter resolves that placeholder, and only to the
quarantine workspace while rendering.

Semantic selection, ordering, omission decisions, digest inputs, and canonical
byte accounting complete before that provider projection, so adding a provider
projection cannot change the selected semantic result. The compiled context is
a derived view and is never written to durable workflow
authority. Pending invocation records retain their existing attribution-only
schema and do not acquire context, objectives, resolved parameters, failure
output, artifact content, or secrets. `plan --expanded` exposes a per-phase,
resume, and repair recipe listing each component's authority source and reason,
plus intentional exclusions; runtime-resolved values and prompt text are not
printed.

Resource metadata grants read access to the full quarantine, identifies
effective phase writes (none for a read-only phase), and lists
protected/runtime-owned exclusions. The Stage 6 scheduler consumes this same
metadata: omitted phase scopes inherit the workflow allowlist, ambiguous or
overlapping scopes serialize, and actual actor changes are checked against the
selected phase scope before import.

### Execution policy and durable budgets

`spec.execution.policy` is normalized before execution and carried through the
provider request as enforced authority, not prompt advice. Network is denied by
default. External capabilities, network access, and credential injection are
privileged effects and require a named human gate whose commit-valued evidence
is still valid at the current `HEAD`. Per-agent policies may only narrow the
workflow policy. Providers must implement both filesystem-boundary and
execution-policy enforcement interfaces; a missing or unsupported capability
fails before the provider runs.

Credential values are read from their declared environment variables only for
the authorized invocation. Invocation context and expanded plans contain names
and limits but never values. The built-in Codex adapter starts from a minimal
bootstrap environment, adds only authorized credentials, and redacts those
values from stdout, stderr, and final-message capture, including values split
across output writes.

Cumulative model-call, tool-call, token, duration, and optional monetary usage
is stored in the runtime-private `runtime/resource-usage` Git record. Calls are
consumed before execution so a crash cannot refund an attempt. Provider-reported
tokens and cost are added afterward; missing metering for a declared usage
limit fails closed. Exhaustion writes the resource name to that record before
returning an error. Restarts therefore return the same terminal budget state
until explicit reset removes it. Caller cancellation propagates through the
existing context; a policy deadline also cancels the provider and records
duration exhaustion.

## Parallel dependency scheduling

Successor workflows may set `spec.execution.maxParallel` from 1 through 32.
Omission resolves to one. At each scheduling decision the engine derives the
ready set exclusively from the immutable dependency graph and durable accepted
phase markers, then selects a declaration-ordered batch whose effective
resource scopes are pairwise disjoint. Read-only phases have an empty write
scope; phases with effective actor commit permission, conditional execution,
or runtime-owned progress/bookkeeping transitions remain serial.

Concurrent providers receive separate quarantine repositories based on the
same authoritative baseline. The first provider failure cancels sibling
contexts. Provider completion does not accept a phase: quarantine imports,
deterministic validations, runtime checkpoints, contract publication, and
phase markers are processed in authored order. A disjoint sibling may advance
authoritative HEAD before another quarantine is imported; the later import is
accepted only when its actual changed paths do not intersect authoritative
changes since the shared baseline.

The active batch and every node's active phase, pending invocation, invocation
outcome, and provider-return state are Git-object/ref-backed. Restart promotes
one node at a time into the ordinary active-phase recovery path, so retained
partial work, bounded repair, terminal safety failures, and acceptance markers
keep their existing meanings. `status` exposes the batch's phase IDs, and
`reset` validates and removes every retained batch quarantine before deleting
its authority records.

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
- ignored-file integrity protection with zero-match failure and link-only
  symlink hashing;
- Markdown checklist progress and one-criterion progress invariants;
- engine-owned criterion advancement by stable `criterionID`, with durable
  before-state and exact-delta enforcement;
- v1alpha4 typed work items with durable exact-target advancement, bounded
  static collection expansion, and an optional Markdown checklist adapter;
- actor-less, durable Markdown checklist/status/index bookkeeping transitions
  that preserve non-bookkeeping content byte-for-byte;
- first-unchecked progress selection and bounded next-unchecked loops;
- named AI phases with provider-neutral actors;
- shell, workspace-policy, Git-checkpoint, file-regex, and Markdown-checklist
  validation tools;
- captured shell output and bounded, redacted validation failure logs;
- durable bounded failure stage/error status for failures outside an active
  phase, cleared after a successful retry;
- durable content-addressed success evidence for equivalent deterministic
  validation requests;
- one bounded repair attempt;
- automatic Git checkpoints of allowed dirty files;
- resumable active phases and commit-aware phase markers;
- interactive human gates with conditional skip, placement prerequisites, and
  durable evidence records;
- conditional flow steps, validation steps, generalized phase lifecycle
  actions, phases, runtime-derived active-phase recovery, and human gates;
- flow assertions for clean workspace and empty progress; and
- completion assertions, final validation, checkpoint, configured completion
  marker, and deterministic summary fields.

Executable v1alpha1 marker values are either omitted or `head_commit`.
Completion summaries accept only `branch`, `base_commit`, `head_commit`,
`state_directory`, `workspace_clean`, `canonical_gate_green`,
`final_gate_green`, `commits_since_base`, and `changed_files_since_base`;
gate-green fields require a configured final validation. Human gates require
declared prerequisite phases and an exact-text acknowledgement with a non-empty
value. v1alpha2 and later store their evidence under the runtime-owned
`human/<gate-id>` identity; only v1alpha1 compatibility documents may configure
record layout or procedural gate actions. These contracts are validated before
a repository is mutated.

The expression evaluator is deliberately small and parsed before execution. It
supports typed literals, a finite list of workflow/state/progress references,
`not`/`and`/`or`, equality and integer comparisons, `default(...)`,
`progress.is_checked(...)`, and bounded validation-log `tail(...)`. It is not a
general-purpose template language: unknown functions, type mismatches, and
unavailable values fail closed.

## Codex adapter

The Codex adapter uses headless `codex exec`. Actor workspaces exclude
`.agentflow/**` and `.agents/skills/agentflow-spec/**`, including ignored and
untracked files. AgentFlow refuses to invoke an actor when either namespace is
tracked in the current Git snapshot because the depth-one actor repository would
otherwise contain that private content in its `HEAD` tree.
Actor-created changes in either namespace are rejected regardless of mutation
allowlists or ignored-control patterns, including within initialized submodules.
Runtime-private integrity paths remain in the authoritative baseline and are
validated there after every provider invocation; the actor checkout validates
only the visible portion of each integrity manifest.

Each actor quarantine is an independent depth-one Git repository at the
authoritative `HEAD`, not a linked worktree. This keeps normal actor Git commands
and authorized actor commits available without exposing authoritative refs,
parent history, or object storage. For actor invocations, the adapter uses a
strict named Codex permissions profile that denies reads from the authoritative
checkout. It ignores user Codex configuration for that isolated invocation so a
configured sandbox
cannot weaken the boundary. `danger-full-access` is rejected when this boundary
is present. Custom providers receive the same provider-neutral filesystem rules.
The engine invokes one only when it implements `FilesystemBoundaryEnforcer` and
explicitly reports that it enforces the boundary; the adapter must then enforce
every rule or reject the request.

The same fail-closed handshake applies to `ExecutionPolicyEnforcer`. The Codex
adapter uses the strict actor profile for network policy, rejects unsupported
external capability names and monetary limits, and does not inherit the parent
process's general environment. Custom providers must enforce every declared
effect and report token/cost usage whenever those budgets are bounded.
Privileged successor policies additionally name a dedicated, unconditional
human preauthorization gate. Its canonical evidence is recorded before phase
recovery or scheduling and checked again at the provider boundary.

The adapter supports the workflow's `never`
approval policy and fails closed for other approval policies rather than silently
ignoring them. It explicitly passes `-c approval_policy="never"`, which overrides
any user configuration for that process. If the authored sandbox is omitted or
empty, the adapter resolves it to the explicit safe default `workspace-write`;
explicit `read-only` and `workspace-write` values select the corresponding base
permissions profile. This default is specific to the built-in Codex provider and
is not imposed on injected or custom providers. The adapter uses
`output_last_message` to decide whether to request final-message capture; any
returned message remains diagnostic/presentation output rather than workflow
evidence or authority.

## Current limits

This is an interpreter MVP, not yet a complete implementation of every field in
the descriptive specification. The following are explicitly non-executable in
this runtime and are rejected by validation: for the built-in Codex provider,
approval policies other than `never`, state backends other than
`git-dir`, non-Git workspaces, non-Markdown progress sources, non-`on-exit` temp
cleanup, and `tool` and `human` phase kinds. Tool types outside the built-in
set require an explicitly supplied plugin registry; an unregistered type,
unsupported plugin contract version, malformed typed configuration, or a
mismatch between a tool's `mutates_workspace` declaration and the plugin's
declared mutation behavior fails before execution.
`recovery.activePhase` is also non-executable: it is accepted structurally only
so the validator can report a source-aware unsupported diagnostic before any
repository access or mutation. Runtime-derived safe resume replaces it.
Parallel DAG scheduling is implemented. Arbitrary programming-language
expressions remain unavailable. Custom deterministic plugins use the explicit
`agentflow.dev/tool/v1` registration contract: the plugin type is registered
without `init`, its config is decoded with strict fields into a Go type, and
its mutation declaration is verified before the plugin can run. Plugin version
compatibility is exact within v1; a future incompatible contract requires a new
versioned registration. Unsupported executable constructs produce an error
rather than being ignored.
Plugin-specific `config` is accepted by successor APIs and rejected by the
grammar-frozen v1alpha1 decoder and schema.

Read-only plugins must declare `MutationNone`; the runtime snapshots HEAD and
the complete implementation worktree immediately around invocation and rejects
any delta regardless of normal mutation allowlists. Durable validation reuse
also requires a valid immutable behavior fingerprint. The complete descriptor,
fingerprint, and authored configuration participate in the evidence key.
Plugins without a fingerprint remain executable but are non-cacheable.

Embedding is supported through the exported `runtime.New` constructor, which
accepts provider implementations and a per-runtime `tool.Registry` explicitly.
There is no global/init registration and no CLI dynamic plugin discovery.
