#!/usr/bin/env bash
set -euo pipefail

# Bootstrap AgentFlow to its self-hosting MVP gate.
#
# IMPORTANT:
#
# This shell script is intentionally temporary orchestration.
#
# Bootstrap:
#   01 Terra/high   - executable schema + validate command
#   02 Luna/high    - conformance + canonical repository gate
#   03 Terra/xhigh  - expressions, conditionals, dynamic control flow
#   04 Terra/xhigh  - recovery, lineage, human/completion parity
#   05 Luna/high    - end-to-end v1alpha1 semantic parity
#   06 Terra/high   - repository-owned self-development workflow
#   07 Luna/high    - self-hosting adversarial/integration/CI coverage
#   08 Terra/xhigh  - bootstrap final audit
#
# Cutover:
#   AgentFlow itself runs:
#       Luna/high   - real AgentFlow implementation change
#       Terra/high  - independent audit
#       Terra/high  - bounded repair, only if deterministic validation fails
#       Human gate
#       Completion
#
# The cutover run is intentionally interrupted after the audit's durable
# checkpoint and then restarted, proving resume without replaying accepted work.
#
# Usage:
#   chmod +x bootstrap-agentflow-mvp.sh
#   ./bootstrap-agentflow-mvp.sh [repo-root]
#
# Optional:
#   TERRA_MODEL=gpt-5.6-terra
#   LUNA_MODEL=gpt-5.6-luna
#   SELFHOST_WAIT_SECONDS=7200
#   RESET_BOOTSTRAP_STATE=1
#
# The self-hosting task can also be overridden:
#
#   SELF_HOST_TASK='Implement a real AgentFlow improvement...' \
#     ./bootstrap-agentflow-mvp.sh

###############################################################################
# Basic configuration
###############################################################################

die() {
  printf '\nERROR: %s\n' "$*" >&2
  exit 1
}

note() {
  printf '\n==> %s\n' "$*"
}

ROOT="${1:-}"

if [[ -z "$ROOT" ]]; then
  ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
fi

[[ -n "$ROOT" && -d "$ROOT/.git" ]] \
  || die "run inside agentflow or pass its repository root"

ROOT="$(cd "$ROOT" && pwd)"
cd "$ROOT"

ROADMAP="ROADMAP.md"

TERRA_MODEL="${TERRA_MODEL:-gpt-5.6-terra}"
LUNA_MODEL="${LUNA_MODEL:-gpt-5.6-luna}"

RESET_BOOTSTRAP_STATE="${RESET_BOOTSTRAP_STATE:-0}"
SELFHOST_WAIT_SECONDS="${SELFHOST_WAIT_SECONDS:-7200}"

# This is deliberately a real AgentFlow source change rather than a synthetic
# fixture change. It also closes one of Priority 3's explicit usability needs.
SELF_HOST_TASK="${SELF_HOST_TASK:-\
Improve 'agentflow status' so a developer can clearly distinguish an active, \
failed/recoverable, human-gated, and completed self-hosted run. Include the \
current/next phase, useful checkpoint or validation context where available, \
and actionable state without exposing private model reasoning. Add deterministic \
tests and update docs/self-hosting.md with a concise dogfooding note explaining \
that this change was implemented through examples/develop-agentflow.agent-workflow.yaml. \
Do not modify ROADMAP.md.}"

STATE_DIR="$(git rev-parse --git-dir)/agentflow-mvp-bootstrap"

BASE_FILE="$STATE_DIR/base"
BRANCH_FILE="$STATE_DIR/branch"
ACTIVE_FILE="$STATE_DIR/active-phase"
BOOTSTRAP_COMPLETE_FILE="$STATE_DIR/bootstrap-complete"
SELFHOST_START_FILE="$STATE_DIR/selfhost-start"
SELFHOST_INTERRUPTED_FILE="$STATE_DIR/selfhost-interrupted"
SELFHOST_COMPLETE_FILE="$STATE_DIR/selfhost-complete"

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/agentflow-mvp.XXXXXX")"

SELFHOST_PID=""

cleanup() {
  if [[ -n "$SELFHOST_PID" ]] && kill -0 "$SELFHOST_PID" 2>/dev/null; then
    kill -TERM "$SELFHOST_PID" 2>/dev/null || true
  fi

  rm -rf "$TMP_ROOT"
}

trap cleanup EXIT INT TERM

LAST_GATE_LOG=""

PHASE_ID=""
PHASE_LABEL=""
PHASE_MODEL=""
PHASE_EFFORT=""
PHASE_REQUIRES_CHANGE="1"
PHASE_PROMPT=""

###############################################################################
# Treat this bootstrap script as local control state if it lives in the repo.
###############################################################################

SCRIPT_ABS="$(
  cd "$(dirname "${BASH_SOURCE[0]}")"
  printf '%s/%s\n' "$(pwd)" "$(basename "${BASH_SOURCE[0]}")"
)"

SCRIPT_REL=""

case "$SCRIPT_ABS" in
  "$ROOT"/*)
    SCRIPT_REL="${SCRIPT_ABS#"$ROOT"/}"
    ;;
esac

is_local_control_file() {
  local file="$1"

  if [[ -n "$SCRIPT_REL" && "$file" == "$SCRIPT_REL" ]]; then
    return 0
  fi

  return 1
}

###############################################################################
# Preconditions
###############################################################################

command -v git >/dev/null 2>&1 || die "git not found"
command -v go >/dev/null 2>&1 || die "go not found"
command -v codex >/dev/null 2>&1 || die "codex CLI not found"

[[ -f "$ROADMAP" ]] \
  || die "ROADMAP.md not found; run from a revision containing the AgentFlow roadmap"

grep -Fq 'Priority 3 — Self-hosting development workflow' "$ROADMAP" \
  || die "ROADMAP.md does not contain the self-hosting MVP gate"

###############################################################################
# Dirty/scope helpers
###############################################################################

dirty_files() {
  {
    git diff --name-only
    git diff --cached --name-only
    git ls-files --others --exclude-standard
  } |
    sed '/^$/d' |
    sort -u |
    while IFS= read -r file; do
      is_local_control_file "$file" && continue
      printf '%s\n' "$file"
    done
}

has_dirty_files() {
  [[ -n "$(dirty_files)" ]]
}

changed_files_since_base() {
  {
    git diff --name-only "$RUN_BASE" HEAD
    git diff --name-only
    git diff --cached --name-only
    git ls-files --others --exclude-standard
  } |
    sed '/^$/d' |
    sort -u |
    while IFS= read -r file; do
      is_local_control_file "$file" && continue
      printf '%s\n' "$file"
    done
}

allowed_change() {
  local file="$1"

  case "$file" in
    cmd/*)
      return 0
      ;;
    internal/*)
      return 0
      ;;
    provider/*)
      return 0
      ;;
    spec/*)
      return 0
      ;;
    examples/*)
      return 0
      ;;
    docs/*)
      return 0
      ;;
    scripts/*)
      return 0
      ;;
    .github/*)
      return 0
      ;;
    testdata/*)
      return 0
      ;;
    README.md|CHANGELOG.md|CONTRIBUTING.md|go.mod|go.sum)
      return 0
      ;;
  esac

  return 1
}

###############################################################################
# Protected content
#
# ROADMAP and research define the target. Agents may implement/document the
# target, but may not quietly move it.
###############################################################################

tracked_group_hash() {
  local pattern="$1"
  local listing=""

  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    listing+="$file $(git hash-object "$file")"$'\n'
  done < <(git ls-files "$pattern")

  printf '%s' "$listing" | git hash-object --stdin
}

record_integrity_baseline() {
  git hash-object "$ROADMAP" > "$STATE_DIR/roadmap.hash"
  tracked_group_hash 'docs/research/**' > "$STATE_DIR/research.hash"

  if [[ -f AGENTS.md ]]; then
    git hash-object AGENTS.md > "$STATE_DIR/agents.hash"
  fi
}

assert_integrity() {
  [[ "$(git hash-object "$ROADMAP")" == "$(cat "$STATE_DIR/roadmap.hash")" ]] \
    || die "ROADMAP.md changed during MVP bootstrap"

  [[ "$(tracked_group_hash 'docs/research/**')" == \
     "$(cat "$STATE_DIR/research.hash")" ]] \
    || die "research material changed during MVP bootstrap"

  if [[ -f "$STATE_DIR/agents.hash" ]]; then
    [[ -f AGENTS.md ]] || die "AGENTS.md disappeared during bootstrap"

    [[ "$(git hash-object AGENTS.md)" == "$(cat "$STATE_DIR/agents.hash")" ]] \
      || die "AGENTS.md changed during MVP bootstrap"
  fi
}

assert_change_scope() {
  local file

  while IFS= read -r file; do
    [[ -n "$file" ]] || continue

    allowed_change "$file" \
      || die "out-of-scope file changed during MVP bootstrap: $file"
  done < <(changed_files_since_base)

  git merge-base --is-ancestor "$RUN_BASE" HEAD \
    || die "HEAD no longer descends from bootstrap base"

  [[ "$(git symbolic-ref --quiet --short HEAD || true)" == "$RUN_BRANCH" ]] \
    || die "current branch changed during MVP bootstrap"

  assert_integrity
}

###############################################################################
# Initialize durable bootstrap state
###############################################################################

if [[ "$RESET_BOOTSTRAP_STATE" == "1" ]]; then
  has_dirty_files \
    && die "RESET_BOOTSTRAP_STATE requires a clean implementation worktree"

  rm -rf "$STATE_DIR"
fi

mkdir -p "$STATE_DIR"

if [[ ! -f "$BASE_FILE" ]]; then
  has_dirty_files \
    && die "first bootstrap run requires a clean implementation worktree"

  branch="$(git symbolic-ref --quiet --short HEAD || true)"
  [[ -n "$branch" ]] || die "start the bootstrap from a named branch"

  git rev-parse HEAD > "$BASE_FILE"
  printf '%s\n' "$branch" > "$BRANCH_FILE"

  record_integrity_baseline

  note "Initialized AgentFlow MVP bootstrap state"
fi

RUN_BASE="$(cat "$BASE_FILE")"
RUN_BRANCH="$(cat "$BRANCH_FILE")"

git cat-file -e "${RUN_BASE}^{commit}" 2>/dev/null \
  || die "bootstrap base no longer exists: $RUN_BASE"

git merge-base --is-ancestor "$RUN_BASE" HEAD \
  || die "current HEAD no longer descends from bootstrap base"

[[ "$(git symbolic-ref --quiet --short HEAD || true)" == "$RUN_BRANCH" ]] \
  || die "current branch differs from bootstrap branch $RUN_BRANCH"

assert_integrity

###############################################################################
# Codex execution
###############################################################################

codex_run() {
  local model="$1"
  local effort="$2"
  local label="$3"
  local last="$TMP_ROOT/${label}.last.txt"

  note "Codex: $label [$model / $effort]"

  codex \
    --ask-for-approval never \
    exec \
    --cd "$ROOT" \
    --sandbox workspace-write \
    --ephemeral \
    --color never \
    --model "$model" \
    -c "model_reasoning_effort=\"$effort\"" \
    --output-last-message "$last" \
    -
}

###############################################################################
# Bootstrap deterministic validation
#
# Before the repository owns scripts/check.sh, use the smallest reasonable
# Go/repository gate. Once scripts/check.sh exists, it becomes authoritative
# for bootstrap acceptance too.
###############################################################################

fallback_gate() {
  local bad_fmt

  git diff --check

  bad_fmt="$(
    find cmd internal provider \
      -type f \
      -name '*.go' \
      -print0 2>/dev/null |
      xargs -0 -r gofmt -l
  )"

  if [[ -n "$bad_fmt" ]]; then
    printf '%s\n' "$bad_fmt" >&2
    return 1
  fi

  go test ...
  go vet ...
}

bootstrap_gate() {
  if [[ -f scripts/check.sh ]]; then
    ./scripts/check.sh
  else
    fallback_gate
  fi
}

run_gate() {
  local label="$1"

  assert_change_scope

  LAST_GATE_LOG="$TMP_ROOT/gate-${label}.log"

  note "Deterministic gate: $label"

  if bootstrap_gate 2>&1 | tee "$LAST_GATE_LOG"; then
    printf '==> Gate passed: %s\n' "$label"
    return 0
  fi

  printf '==> Gate failed: %s\n' "$label" >&2
  return 1
}

gate_with_one_repair() {
  local label="$1"
  local context="$2"
  local failed_log

  if run_gate "$label"; then
    return 0
  fi

  failed_log="$LAST_GATE_LOG"

  {
    cat <<PROMPT
The deterministic AgentFlow bootstrap gate failed.

Current bounded task:
${context}

Inspect the repository state and repair only this task.

Do not alter ROADMAP.md or research material. Do not weaken validation merely
to make the failure disappear. Preserve correct partial work.

The failing gate output follows.

--- gate output ---
PROMPT

    tail -n 260 "$failed_log"
  } | codex_run \
        "$TERRA_MODEL" \
        "high" \
        "repair-${label}"

  assert_change_scope

  run_gate "${label}-after-repair" \
    || die "gate still fails after one Terra repair attempt: $label"
}

###############################################################################
# Git checkpointing
###############################################################################

ensure_clean_checkpoint() {
  local label="$1"
  local staged=0
  local file

  assert_change_scope

  if has_dirty_files; then
    note "Checkpointing successful bootstrap work: $label"

    while IFS= read -r file; do
      [[ -n "$file" ]] || continue

      allowed_change "$file" \
        || die "refusing to checkpoint disallowed file: $file"

      git add -A -- "$file"
      staged=1
    done < <(dirty_files)

    (( staged == 1 )) \
      || die "dirty work existed but nothing was eligible to checkpoint"

    git commit -m "Bootstrap AgentFlow MVP: ${label}"
  fi

  has_dirty_files \
    && die "implementation worktree remains dirty after checkpoint"

  assert_change_scope
}

###############################################################################
# Bootstrap phase state
###############################################################################

phase_marker() {
  printf '%s/phases/%s.done' "$STATE_DIR" "$1"
}

phase_done() {
  local id="$1"
  local marker
  local sha

  marker="$(phase_marker "$id")"
  [[ -f "$marker" ]] || return 1

  sha="$(cat "$marker")"

  git cat-file -e "${sha}^{commit}" 2>/dev/null || return 1
  git merge-base --is-ancestor "$sha" HEAD
}

write_active_phase() {
  local id="$1"
  local start="$2"

  {
    printf '%s\n' "$id"
    printf '%s\n' "$start"
  } > "$ACTIVE_FILE"
}

mark_phase_done() {
  local id="$1"

  mkdir -p "$STATE_DIR/phases"
  git rev-parse HEAD > "$(phase_marker "$id")"
  rm -f "$ACTIVE_FILE"

  printf '==> Bootstrap phase %s complete at %s\n' \
    "$id" \
    "$(git rev-parse --short HEAD)"
}

###############################################################################
# Phase definitions
###############################################################################

configure_phase() {
  local id="$1"

  PHASE_ID="$id"
  PHASE_REQUIRES_CHANGE="1"

  case "$id" in

    ###########################################################################
    # Priority 1 — executable schema + validator
    ###########################################################################

    01)
      PHASE_LABEL="executable-schema-validator"
      PHASE_MODEL="$TERRA_MODEL"
      PHASE_EFFORT="high"

      PHASE_PROMPT="$(cat <<'PROMPT'
Read ROADMAP.md Priority 1, the v1alpha1 field guide, reference schema YAML,
runtime documentation, and current workflow loader/model.

Implement the executable-schema and validation foundation.

Concentrate on the architectural gap rather than rewriting documentation prose.

The CLI must gain a real `agentflow validate -f ...` path that performs no
workspace mutation and does not require constructing a runnable engine merely
to identify document errors.

Establish one authoritative executable schema/model used by the loader,
validator, conformance tests, and documentation checks. Avoid maintaining an
independent handwritten validator that can silently drift from decoding.

Validation must cover structural typing and cross-references needed before
execution, and unknown executable fields must fail rather than be silently
ignored.

Preserve source location information sufficiently to make diagnostics useful.
Prefer YAML paths plus line/column when available.

Explicitly distinguish:
- invalid;
- valid and executable;
- syntactically/spec-valid but intentionally unsupported by this runtime,

where that distinction remains necessary during the bootstrap.

Do not implement later DAG or authoring-syntax work.

Add focused positive and negative tests before considering this complete.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 1 — fixtures, diagnostics, canonical quality gate
    ###########################################################################

    02)
      PHASE_LABEL="conformance-and-quality-gate"
      PHASE_MODEL="$LUNA_MODEL"
      PHASE_EFFORT="high"

      PHASE_PROMPT="$(cat <<'PROMPT'
Continue Priority 1 using the validation architecture now present.

Build a useful conformance corpus and repository-owned deterministic quality
gate.

Add compact valid/invalid/unsupported workflow fixtures covering references,
unknown executable fields, malformed types, duplicate identifiers, invalid
expressions/references, and representative shipped examples.

Make diagnostics stable enough to test meaningfully without overspecifying
incidental wording.

Create a repository-owned `scripts/check.sh` as the canonical deterministic
gate for AgentFlow development. It should check formatting/diff hygiene, Go
tests, vet, appropriate race coverage, and validate shipped AgentWorkflow
definitions using the AgentFlow validator itself.

Add CI that delegates to that repository-owned gate rather than duplicating
validation policy in workflow YAML.

Keep live model calls out of CI.

Do not work ahead into SDL ergonomics or DAG execution.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 2 — expression/control-flow parity
    ###########################################################################

    03)
      PHASE_LABEL="runtime-control-flow-parity"
      PHASE_MODEL="$TERRA_MODEL"
      PHASE_EFFORT="xhigh"

      PHASE_PROMPT="$(cat <<'PROMPT'
Read ROADMAP.md Priority 2, docs/agentflow-v1alpha1.md, docs/runtime.md, the
reference definition, the Priority 5 example, and the current interpreter.

Close the v1alpha1 control-flow/runtime gap.

Focus this phase on:
- deliberately bounded expression semantics;
- typed parameter/default/environment/override behavior;
- condition evaluation;
- bounded dynamic loops such as next-unchecked criterion;
- phase/gate/step conditional execution;
- progress selection and invariants;
- unsupported-expression failure behavior.

The expression language should be explicit and testable, not a growing series
of string substitutions or special cases.

Do not introduce a general-purpose programming language and do not begin DAG
scheduling.

Remove Priority-5-specific assumptions encountered in these control paths by
generalizing the semantic concept, not by adding another example-specific case.

Use table-driven and end-to-end fixtures covering success, false conditions,
bad references/types, loop bounds, and progress violations.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 2 — durable state/recovery parity
    ###########################################################################

    04)
      PHASE_LABEL="runtime-recovery-parity"
      PHASE_MODEL="$TERRA_MODEL"
      PHASE_EFFORT="xhigh"

      PHASE_PROMPT="$(cat <<'PROMPT'
Continue Priority 2, concentrating on durable execution semantics.

Audit the interpreter against the documented contracts for:
- initialization;
- namespaced Git-backed state;
- active phase;
- phase markers;
- checkpointing;
- branch/base lineage;
- validation failure and bounded repair;
- human gates;
- completion assertions;
- reset;
- interrupted execution and resume.

Recovery must preserve useful accepted commits and partial work without
pretending incomplete work was accepted.

Exercise:
- interruption before checkpoint;
- interruption after checkpoint;
- dirty partial state;
- agent-created commits;
- validation interrupted/failing;
- invalidated phase marker;
- branch change;
- detached HEAD;
- rebase/non-ancestor state;
- exhausted repair budget;
- human gate restart;
- completion restart/idempotence.

Prefer integration tests with temporary Git repositories and deterministic fake
providers/tools. Do not require live model calls.

State refs for two workflow names must not collide.

Do not begin DAG/parallel execution.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 2 — semantic parity of the concrete reference workflow
    ###########################################################################

    05)
      PHASE_LABEL="v1alpha1-end-to-end-parity"
      PHASE_MODEL="$LUNA_MODEL"
      PHASE_EFFORT="high"

      PHASE_PROMPT="$(cat <<'PROMPT'
Perform the end-to-end v1alpha1 runtime-parity pass.

Use the existing concrete Priority 5 AgentWorkflow as a demanding compatibility
fixture, but do not require the Mothership repository or a live model call for
automated tests.

Build deterministic fixture repositories/providers as necessary to prove that
the interpreter can execute the workflow semantics represented there:

- preconditions and protected boundaries;
- progress targeting;
- condition/flow semantics;
- validation;
- bounded repair;
- checkpoints;
- resume;
- human verification;
- completion.

Find and remove remaining behavior that exists only because of the historical
shell orchestrator rather than because v1alpha1 defines that semantic concept.

Update docs/runtime.md so its supported/unsupported list matches reality.

At the end, every executable construct documented as v1alpha1 should either be
implemented or explicitly classified non-executable by the validator/runtime.

Do not begin Priority 4 syntax simplification or Priority 5 DAG work.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 3 — create the self-hosting workflow
    ###########################################################################

    06)
      PHASE_LABEL="self-hosting-workflow"
      PHASE_MODEL="$TERRA_MODEL"
      PHASE_EFFORT="high"

      PHASE_PROMPT="$(cat <<'PROMPT'
Read ROADMAP.md Priority 3 and build the repository-owned self-development
workflow.

Create:

    examples/develop-agentflow.agent-workflow.yaml

with:

    metadata.name: develop-agentflow

and a string parameter named:

    task

The workflow must be genuinely usable for future AgentFlow development, not
hard-coded to one bootstrap task.

Use the normal Go interpreter and existing portable v1alpha1 semantics.

The workflow should contain two explicit AI phases with stable IDs:

    implement
    audit

Use:
- Luna/high for the bounded implementation phase;
- Terra/high for the independent audit;
- Terra/high for at most one validation repair attempt.

The implementation prompt should receive `parameters.task`.
The audit must inspect actual repository state rather than accepting the
implementer's completion claim.

The AgentFlow YAML—not a shell wrapper—must own:

- allowed mutation scope;
- protected repository resources;
- deterministic validation;
- one-repair budget;
- checkpointing;
- interrupted-phase recovery;
- human confirmation;
- completion.

Use the repository's scripts/check.sh as the deterministic quality gate.

Protect at least:
- ROADMAP.md;
- research documents;
- scripts/check.sh;
- the self-host workflow definition itself;
- CI validation policy.

Do not allow an agent to weaken those files during a run.

The human gate must have stable id:

    self-host-review

and require exact acknowledgement:

    yes

A run should be allowed to modify the ordinary AgentFlow source/spec/docs paths
needed for a bounded development task.

Add docs/self-hosting.md describing how to validate, run, status, resume, and
reset this workflow.

Ensure `agentflow status` can inspect this run even before the later dogfooded
status enhancement.

Do not perform the actual proof-of-self-hosting change in this phase. Build the
vehicle and tests for it.
PROMPT
)"
      ;;

    ###########################################################################
    # Priority 3 — hostile/error-path tests and CI
    ###########################################################################

    07)
      PHASE_LABEL="self-hosting-conformance"
      PHASE_MODEL="$LUNA_MODEL"
      PHASE_EFFORT="high"

      PHASE_PROMPT="$(cat <<'PROMPT'
Harden the self-hosting MVP before a live dogfood run.

Use deterministic fake providers and temporary Git repositories. No live model
calls belong in the automated suite.

Prove at least:

- examples/develop-agentflow.agent-workflow.yaml validates;
- its implementation and audit phases are distinct executions;
- the declared repair actor is invoked no more than its configured budget;
- a deliberately failing validation cannot advance;
- an attempted ROADMAP.md mutation is rejected;
- an attempted scripts/check.sh mutation is rejected;
- another out-of-scope mutation is rejected;
- valid agent-created commits are preserved;
- valid dirty work is checkpointed;
- killing/restarting after a phase checkpoint does not replay that phase;
- state for two workflow names does not collide;
- human-gate evidence is durable;
- completion is idempotent;
- reset affects only the workflow's AgentFlow state;
- status is meaningful in active, validation-failed/recoverable,
  human-gated, and completed fixtures.

Update CI so the self-hosting definition and these deterministic runtime
semantics are continuously exercised without Codex/model execution.

Do not loosen the workflow merely to simplify a fixture.
PROMPT
)"
      ;;

    ###########################################################################
    # Bootstrap final audit
    ###########################################################################

    08)
      PHASE_LABEL="pre-self-host-audit"
      PHASE_MODEL="$TERRA_MODEL"
      PHASE_EFFORT="xhigh"
      PHASE_REQUIRES_CHANGE="0"

      PHASE_PROMPT="$(cat <<'PROMPT'
Audit AgentFlow against ROADMAP.md Priorities 1, 2, and the pre-dogfood portion
of Priority 3.

Do not begin later roadmap priorities.

Verify as a system:

- validation occurs before mutable execution;
- schema/model/validator semantics do not drift independently;
- unknown executable constructs fail closed;
- v1alpha1 documented executable constructs have runtime parity;
- bounded expressions/conditions/loops cannot escape their contract;
- progress invariants are deterministic;
- validation owns advancement;
- repair budget exhaustion is authoritative;
- Git state and recovery are idempotent;
- accepted phase markers survive restart without replay;
- stale/non-ancestor state is rejected;
- human and completion state are durable;
- the self-host workflow is generic enough for real AgentFlow development;
- its implement/audit/repair actors have the intended model roles;
- protected resources cannot be weakened by an acting agent;
- scripts/check.sh and CI provide deterministic coverage without live models.

Run representative self-hosting fixtures with fake providers.

Repair genuine defects only. The actual live self-development proof is owned by
the next AgentFlow-run stage, not by this bootstrap audit.
PROMPT
)"
      ;;

    *)
      die "unknown bootstrap phase: $id"
      ;;
  esac
}

###############################################################################
# Finish/run/resume bootstrap phases
###############################################################################

finish_phase() {
  local id="$1"
  local phase_start="$2"

  assert_change_scope

  gate_with_one_repair \
    "${id}-${PHASE_LABEL}" \
    "$PHASE_LABEL"

  ensure_clean_checkpoint "$PHASE_LABEL"

  if [[ "$PHASE_REQUIRES_CHANGE" == "1" ]]; then
    if git diff --quiet "$phase_start" HEAD; then
      die "phase $id ($PHASE_LABEL) produced no net repository change"
    fi
  fi

  mark_phase_done "$id"
}

resume_active_phase_if_needed() {
  [[ -f "$ACTIVE_FILE" ]] || return 0

  local active_id
  local active_start

  active_id="$(sed -n '1p' "$ACTIVE_FILE")"
  active_start="$(sed -n '2p' "$ACTIVE_FILE")"

  [[ -n "$active_id" ]] \
    || die "active bootstrap phase is missing its id"

  git cat-file -e "${active_start}^{commit}" 2>/dev/null \
    || die "active bootstrap phase start commit no longer exists"

  git merge-base --is-ancestor "$active_start" HEAD \
    || die "HEAD no longer descends from active bootstrap phase start"

  configure_phase "$active_id"

  note "Resuming bootstrap phase $active_id: $PHASE_LABEL"

  {
    cat <<'PROMPT'
Resume this bootstrap phase from the repository state already present.

The previous execution may have committed part or all of the correct change or
left valid dirty work. Inspect existing state first. Preserve correct work and
finish only the phase objective below.

PROMPT

    printf '%s\n' "$PHASE_PROMPT"
  } | codex_run \
        "$PHASE_MODEL" \
        "$PHASE_EFFORT" \
        "resume-${active_id}-${PHASE_LABEL}"

  finish_phase "$active_id" "$active_start"
}

run_phase() {
  local id="$1"
  local start

  configure_phase "$id"

  if phase_done "$id"; then
    printf '==> Skipping completed bootstrap phase %s: %s\n' \
      "$id" \
      "$PHASE_LABEL"
    return 0
  fi

  has_dirty_files \
    && die "phase $id cannot start with unexplained dirty implementation files"

  start="$(git rev-parse HEAD)"

  write_active_phase "$id" "$start"

  note "Bootstrap phase $id: $PHASE_LABEL"

  printf '%s\n' "$PHASE_PROMPT" |
    codex_run \
      "$PHASE_MODEL" \
      "$PHASE_EFFORT" \
      "${id}-${PHASE_LABEL}"

  finish_phase "$id" "$start"
}

###############################################################################
# Bootstrap
###############################################################################

note "Repository: $ROOT"
printf 'Branch:         %s\n' "$RUN_BRANCH"
printf 'Bootstrap base: %s\n' "$RUN_BASE"
printf 'Current HEAD:   %s\n' "$(git rev-parse HEAD)"

if [[ ! -f "$BOOTSTRAP_COMPLETE_FILE" ]]; then
  resume_active_phase_if_needed

  has_dirty_files \
    && die "worktree is dirty outside a recoverable bootstrap phase"

  run_gate "bootstrap-starting-state" \
    || die "repository did not pass its starting deterministic gate"

  run_phase 01
  run_phase 02
  run_phase 03
  run_phase 04
  run_phase 05
  run_phase 06
  run_phase 07
  run_phase 08

  run_gate "pre-cutover-final" \
    || die "pre-cutover deterministic gate failed"

  ensure_clean_checkpoint "pre-cutover"

  git rev-parse HEAD > "$BOOTSTRAP_COMPLETE_FILE"

  note "Shell-owned bootstrap is complete; cutting over to AgentFlow"
else
  note "Shell-owned bootstrap already completed"
fi

###############################################################################
# Build and validate the interpreter that will own the self-hosting run
###############################################################################

SELFHOST_WORKFLOW="examples/develop-agentflow.agent-workflow.yaml"

[[ -f "$SELFHOST_WORKFLOW" ]] \
  || die "$SELFHOST_WORKFLOW was not created"

grep -Eq '^[[:space:]]*name:[[:space:]]*develop-agentflow[[:space:]]*$' \
  "$SELFHOST_WORKFLOW" \
  || die "self-host workflow metadata.name must be develop-agentflow"

BIN="$STATE_DIR/agentflow-bootstrap-bin"

note "Building AgentFlow for cutover"

go build -o "$BIN" .

"$BIN" validate \
  -f "$SELFHOST_WORKFLOW" \
  -C "$ROOT"

./scripts/check.sh

has_dirty_files \
  && die "cutover requires a clean worktree"

###############################################################################
# Initialize the live self-hosting proof
###############################################################################

if [[ ! -f "$SELFHOST_START_FILE" ]]; then
  git rev-parse HEAD > "$SELFHOST_START_FILE"

  note "Starting first real AgentFlow self-development run"
fi

SELFHOST_BASE="$(cat "$SELFHOST_START_FILE")"

git cat-file -e "${SELFHOST_BASE}^{commit}" 2>/dev/null \
  || die "saved self-hosting base no longer exists"

git merge-base --is-ancestor "$SELFHOST_BASE" HEAD \
  || die "HEAD no longer descends from the self-hosting base"

###############################################################################
# First leg:
#
# Let AgentFlow execute its real implementation and audit. Once the public
# status reaches human-gated, kill the interpreter before the human/completion
# stage. The runtime derives that state only after implementation and audit
# acceptance, so the bootstrap does not need to know the runtime's state refs or
# configured record names.
#
# This is intentionally shell-observed but NOT shell-orchestrated: phase order,
# validation, repair, checkpointing, and acceptance are all AgentFlow-owned.
###############################################################################

interrupt_after_audit_checkpoint() {
  local fifo="$TMP_ROOT/selfhost.stdin"
  local log="$STATE_DIR/selfhost-first-leg.log"
  local elapsed=0
  local state=""

  rm -f "$fifo"
  mkfifo "$fifo"

  # Open FIFO read/write in the parent so the child does not see immediate EOF
  # when it reaches the human gate.
  exec 9<> "$fifo"

  note "AgentFlow self-host leg 1: run through audit checkpoint"

  "$BIN" run \
    -f "$SELFHOST_WORKFLOW" \
    -C "$ROOT" \
    --set "task=$SELF_HOST_TASK" \
    < "$fifo" \
    > "$log" \
    2>&1 &

  SELFHOST_PID=$!

  while (( elapsed < SELFHOST_WAIT_SECONDS )); do
    state="$("$BIN" status -f "$SELFHOST_WORKFLOW" -C "$ROOT" 2>/dev/null |
      awk -F': ' '$1 == "state" { print $2; exit }' || true)"

    if [[ "$state" == "completed" ]]; then
      cat "$log" >&2
      die "self-host workflow completed before the required interruption/human gate"
    fi

    if [[ "$state" == "human-gated" ]]; then
      note "Durable human-gated state observed; interrupting interpreter"

      kill -TERM "$SELFHOST_PID" 2>/dev/null || true

      for _ in $(seq 1 30); do
        if ! kill -0 "$SELFHOST_PID" 2>/dev/null; then
          break
        fi
        sleep 1
      done

      if kill -0 "$SELFHOST_PID" 2>/dev/null; then
        kill -KILL "$SELFHOST_PID" 2>/dev/null || true
      fi

      wait "$SELFHOST_PID" 2>/dev/null || true
      SELFHOST_PID=""

      exec 9>&-
      rm -f "$fifo"

      git rev-parse HEAD > "$SELFHOST_INTERRUPTED_FILE"

      printf '\n==> AgentFlow was intentionally stopped after accepted audit state.\n'
      return 0
    fi

    if ! kill -0 "$SELFHOST_PID" 2>/dev/null; then
      wait "$SELFHOST_PID" 2>/dev/null || true
      SELFHOST_PID=""

      cat "$log" >&2
      die "self-hosting run exited before reaching durable human-gated state (state=${state:-unknown})"
    fi

    sleep 1
    elapsed=$((elapsed + 1))
  done

  kill -TERM "$SELFHOST_PID" 2>/dev/null || true
  wait "$SELFHOST_PID" 2>/dev/null || true
  SELFHOST_PID=""

  cat "$log" >&2
  die "timed out waiting for AgentFlow human-gated state"
}

if [[ ! -f "$SELFHOST_INTERRUPTED_FILE" && ! -f "$SELFHOST_COMPLETE_FILE" ]]; then
  state="$("$BIN" status -f "$SELFHOST_WORKFLOW" -C "$ROOT" |
    awk -F': ' '$1 == "state" { print $2; exit }')"

  case "$state" in
    uninitialized|ready|active|validation-failed/recoverable)
      interrupt_after_audit_checkpoint
      ;;
    human-gated)
      # A previous bootstrap process may have observed the public checkpoint
      # just before it stopped, so preserve that durable progress locally.
      git rev-parse HEAD > "$SELFHOST_INTERRUPTED_FILE"
      ;;
    completed)
      # A previous bootstrap process may have died after AgentFlow completed
      # but before it wrote its local proof marker. The public status is the
      # durable source of truth, so resume the shell proof from there.
      git rev-parse HEAD > "$SELFHOST_COMPLETE_FILE"
      ;;
    *)
      die "unexpected AgentFlow self-host state: ${state:-unknown}"
      ;;
  esac
fi

###############################################################################
# Verify the interrupted state
###############################################################################

if [[ ! -f "$SELFHOST_COMPLETE_FILE" ]]; then
  note "Inspecting interrupted AgentFlow state"

  "$BIN" status \
    -f "$SELFHOST_WORKFLOW" \
    -C "$ROOT" \
    | tee "$STATE_DIR/status-after-interrupt.log"

  #############################################################################
  # Resume:
  #
  # Feed the human acknowledgement. AgentFlow must skip accepted implement/audit
  # work, consume the human gate, and own the completion transition.
  #############################################################################

  note "Resuming AgentFlow from durable state"

  printf 'yes\n' |
    "$BIN" run \
      -f "$SELFHOST_WORKFLOW" \
      -C "$ROOT" \
      --set "task=$SELF_HOST_TASK"

  state="$("$BIN" status -f "$SELFHOST_WORKFLOW" -C "$ROOT" |
    awk -F': ' '$1 == "state" { print $2; exit }')"
  [[ "$state" == "completed" ]] \
    || die "self-host workflow did not reach completed state: ${state:-unknown}"

  git rev-parse HEAD > "$SELFHOST_COMPLETE_FILE"
fi

###############################################################################
# Final proof assertions
###############################################################################

note "Verifying self-hosting MVP proof"

state="$("$BIN" status -f "$SELFHOST_WORKFLOW" -C "$ROOT" |
  awk -F': ' '$1 == "state" { print $2; exit }')"
[[ "$state" == "completed" ]] \
  || die "AgentFlow self-host workflow is not completed: ${state:-unknown}"

git merge-base --is-ancestor "$SELFHOST_BASE" HEAD \
  || die "self-host result no longer descends from its starting commit"

if git diff --quiet "$SELFHOST_BASE" HEAD; then
  die "self-host run produced no real AgentFlow repository change"
fi

[[ -f docs/self-hosting.md ]] \
  || die "self-hosting documentation was not retained"

# The real source change must have been committed by AgentFlow's checkpoint
# semantics rather than left dirty for this shell to normalize.
has_dirty_files \
  && die "self-host workflow completed with dirty implementation state"

###############################################################################
# Rebuild from the code AgentFlow just changed.
###############################################################################

FRESH_BIN="$STATE_DIR/agentflow-post-selfhost-bin"

go build -o "$FRESH_BIN" .

"$FRESH_BIN" validate \
  -f "$SELFHOST_WORKFLOW" \
  -C "$ROOT"

note "Final AgentFlow status"

"$FRESH_BIN" status \
  -f "$SELFHOST_WORKFLOW" \
  -C "$ROOT" \
  | tee "$STATE_DIR/final-status.log"

grep -Eiq 'complete|completed' "$STATE_DIR/final-status.log" \
  || die "final status output does not make completed state apparent"

###############################################################################
# Final repository-owned deterministic gate
###############################################################################

note "Final repository-owned validation"

./scripts/check.sh

git diff --check

has_dirty_files \
  && die "working tree is dirty after final validation"

assert_integrity
assert_change_scope

###############################################################################
# Summary
###############################################################################

printf '\n============================================================\n'
printf 'AgentFlow self-hosting MVP gate reached.\n'
printf '============================================================\n'

printf 'Bootstrap base:  %s\n' "$RUN_BASE"
printf 'Self-host base:  %s\n' "$SELFHOST_BASE"
printf 'Current HEAD:    %s\n' "$(git rev-parse HEAD)"
printf 'Branch:          %s\n' "$RUN_BRANCH"
printf 'Bootstrap state: %s\n' "$STATE_DIR"

printf '\nBootstrap + self-host commits:\n'
git --no-pager log \
  --oneline \
  --decorate \
  "${RUN_BASE}..HEAD"

printf '\nSelf-hosted change only:\n'
git --no-pager log \
  --oneline \
  --decorate \
  "${SELFHOST_BASE}..HEAD"

printf '\nMVP evidence:\n'
printf '  [x] executable workflow validation\n'
printf '  [x] v1alpha1 runtime parity bootstrap\n'
printf '  [x] repository-owned self-development workflow\n'
printf '  [x] real AI implementation phase run by AgentFlow\n'
printf '  [x] independent audit run by AgentFlow\n'
printf '  [x] deterministic AgentFlow-owned validation\n'
printf '  [x] bounded repair policy exercised by deterministic tests\n'
printf '  [x] protected/out-of-scope mutation rejection tested\n'
printf '  [x] accepted implementation/audit checkpointed\n'
printf '  [x] interpreter intentionally interrupted after checkpoint\n'
printf '  [x] restart preserved accepted implementation/audit state without replay\n'
printf '  [x] durable human confirmation\n'
printf '  [x] durable completion marker\n'
printf '  [x] fresh interpreter built from self-hosted source\n'
printf '  [x] repository-owned final gate green\n'
printf '  [x] clean working tree\n'

printf '\nThe bootstrap shell is no longer required to orchestrate subsequent\n'
printf 'AgentFlow development. Future bounded work can start with:\n\n'

printf '  go run . run \\\n'
printf '    -f examples/develop-agentflow.agent-workflow.yaml \\\n'
printf '    -C . \\\n'
printf '    --set "task=<development task>"\n\n'
