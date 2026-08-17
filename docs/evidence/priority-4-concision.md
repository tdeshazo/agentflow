# Priority 4 concision evidence

Status: Passed

This record compares the pre-migration benchmark definitions at immutable
commit `84a11ec` (the last pre-migration benchmark revision) with the migrated
definitions in the checkout:

- `examples/develop-agentflow.agent-workflow.yaml`
- `examples/finish-priority-05.agent-workflow.yaml`

## Measurement method

The comparison uses a source-line count for the authored YAML surface. It
reads the old version with `git show 84a11ec:<path>` and the new version from
the working tree, then counts nonblank, non-comment lines while excluding the
contents of `prompt: |` blocks. This measures orchestration/configuration
surface without allowing domain prompt length to determine the result. The
method is intentionally simple and reproducible; no lines were removed or
reclassified to reach a target percentage.

The command used was equivalent to:

```sh
awk '
function indent(s, n) { n = match(s, /[^ ]/); return n ? n - 1 : 9999 }
BEGIN { in_prompt = 0; count = 0 }
{
  level = indent($0)
  if (!in_prompt && $0 ~ /^[ ]*prompt:[ ]*\|[ ]*$/) {
    in_prompt = 1
    prompt_level = level
    next
  }
  if (in_prompt) {
    if ($0 ~ /^[ ]*$/ || level > prompt_level) next
    in_prompt = 0
  }
  if ($0 ~ /^[ ]*$/ || $0 ~ /^[ ]*#/) next
  count++
}
END { print count }
'
```

## Measured result

| Workflow | Total lines before → after | Non-prompt lines before → after | Reduction |
| --- | ---: | ---: | ---: |
| self-hosting development | 387 → 275 | 324 → 217 | 107 lines (33.0%) |
| Priority 5 combat | 906 → 733 | 516 → 389 | 127 lines (24.6%) |
| combined | 1,293 → 1,008 | 840 → 606 | 234 lines (27.9%) |

Both definitions are materially smaller by this measure. The reduction is
large enough to remove repeated orchestration mechanics while retaining the
full domain prompts and policy declarations.

## Removed or consolidated boilerplate

- Runtime-created temporary directories and per-invocation gate-log paths
  were removed from both definitions.
- Repeated Git state record names, initialization captures, resume fields, and
  explicit saved-base/branch/integrity preconditions were removed. Runtime
  defaults and the single workspace mutation policy now own those checks.
  Priority 5 retains only the named human-confirmation record because it is
  observable evidence, not interpreter plumbing.
- Repeated `assert-change-scope` validation steps and completion assertions
  were removed. Scope, lineage, protected integrity, and cleanliness remain
  runtime enforcement points around actor work, tools, recovery, checkpoint,
  and acceptance.
- Duplicated runner, sandbox, approval, ephemeral, color, commit, and output
  settings moved to `defaults.agent`; phase-kind actor/reasoning/change
  behavior moved to `defaults.phases`.
- Safe-resume lifecycle and the bounded repair actor/prompt moved to
  `defaults.lifecycle` and `defaults.repair`. A concise `repair: once` still
  reruns the same deterministic validation steps after repair.
- Priority 5's model bookkeeping phase became an actor-less phase with two
  constrained transitions: the roadmap `Status` line and the Priority 5
  index checklist item.
- Criterion phases now name stable IDs and set `advanceProgress: true`.
  Deterministic acceptance checks and advances the targeted criterion; prompts
  no longer ask an actor to edit its own acceptance checkbox.

## Representative expanded-plan evidence

Both plans were generated without actor or mutable-tool execution:

```sh
go run ./cmd/agentflow plan --expanded -f examples/develop-agentflow.agent-workflow.yaml
go run ./cmd/agentflow plan --expanded -f examples/finish-priority-05.agent-workflow.yaml
```

Each plan exposes:

- `resolvedLifecycle: policy: safe-resume`, with the selected deterministic
  validation;
- safety enforcement before/after actor and tool work, before checkpoint,
  before marker reuse, and during interrupted-phase recovery;
- recovery in which a valid completed marker wins, actor-completed phases
  resume deterministic acceptance without replaying the actor, and safety
  failures remain terminal;
- validation steps plus the one repair attempt and deterministic rerun;
- runtime checkpoint, lineage, integrity, scope, and cleanliness behavior;
- human-gate and completion contracts.

The Priority 5 plan additionally reports six transitions of the form
`<phase-id>: engine advances only criterionID after validation`. Its final
phase is shown as `kind: bookkeeping`, `validation: phaseGate`, with
`deterministic bookkeeping` in its acceptance sequence and no actor step.

The expanded representation therefore makes the authored reduction inspectable:
the compact YAML omits lifecycle and recovery procedure, but the normalized
contract still distinguishes actor authority, workspace mutation authority,
and deterministic validation/bookkeeping authority.
