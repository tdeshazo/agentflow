# Execution stage 2 runtime security closure

Date: 2026-09-01

This is the durable closeout record for execution stage 2 in
[`ROADMAP.md`](../../ROADMAP.md). It is bound to the checkout containing this
file by the checked-in schemas, conformance fixtures, tests, and verification
commands below; it does not embed a repository hash that would become stale in
the same documentation change.

## Closure matrix

| Requirement | Repository evidence |
| --- | --- |
| Independent actor repository | `internal/gitstate/actor_worktree.go` creates a detached depth-one repository; `actor_private.go` removes runtime-private paths, and `internal/engine/runtime_phase.go` denies the authoritative checkout through a mandatory provider filesystem boundary. Actor changes are imported only after lineage, scope, integrity, permission, and runtime-owned-path checks. |
| Exclusive owner and stale recovery | `internal/engine/run_lease.go` atomically claims `owner` using PID plus kernel start identity, rejects a verified live process, refuses unverifiable process metadata, CAS-replaces only verified stale owners, and compare-and-deletes on release. `internal/gitstate/process*.go` distinguishes unavailable inspection from a verified exit. |
| Executor effects and narrowing | `internal/workflow/execution_policy.go` validates network, capability, credential, approval, and budget policy and rejects every executor-local expansion. `provider/provider.go` requires an execution-policy enforcer; `provider/codex/codex.go` translates supported policy into a strict Codex process boundary and rejects unsupported capability or monetary enforcement. |
| Credentials and redaction | Credentials are resolved only in `internal/engine/resource_budget.go`, carried outside invocation context, and injected into a minimal Codex child environment. The Codex adapter redacts credential values from stdout, stderr, and final-message capture across output chunk boundaries. Run identity stores only a digest of selected environment inputs. |
| Privileged approval | Network access, credentials, and external capabilities require a dedicated, unconditional `approvalGate`. Before successor recovery or scheduling can invoke an affected phase or repair actor, the engine records or verifies the gate's current commit-valued `human/<gate>` evidence. Provider authorization verifies that evidence again before credentials are resolved or an invocation starts. |
| Durable budgets and cancellation | `internal/engine/resource_budget.go` stores versioned usage under runtime-private `runtime/resource-usage`, reserves model/tool calls before execution, records provider tokens/cost, applies workflow and executor limits, derives deadlines, and persists exhaustion before returning. Existing validation repair budgets remain gate-scoped and pre-consumed. Context cancellation reaches providers and shell tools. |
| Inspectability and conformance | Generated v1alpha2-v1alpha4 schemas include the optional policy surface. `plan --expanded` exposes normalized and effective policy without credential values. `internal/workflow/testdata/conformance` includes a valid privileged/narrowed policy and a prompt-injection-style executor escalation that fails validation. |

## Exit criteria

- Actors receive neither authoritative repository history nor runtime-private
  workflow controls through their execution workspace and cannot import work
  until runtime safety checks pass.
- The owner lease prevents concurrent advancement and distinguishes verified
  exit, verified identity mismatch, live ownership, and unavailable inspection.
- Provider requests contain engine-derived authority. Prompt text cannot add a
  network grant, capability, credential, approval, or budget.
- Only named credentials enter the provider process; their values do not enter
  context, state, expanded plans, or durable output.
- Exhaustion is a durable resource record, so restart cannot produce an
  uncontrolled retry or refund a pre-consumed call.

## Verification

```sh
GOCACHE=/tmp/agentflow-stage2-gocache go test ./internal/workflow ./provider/... ./internal/gitstate -count=1
GOCACHE=/tmp/agentflow-stage2-gocache go test ./internal/engine -run 'ExecutionPolicy|ResourceBudget|RunLease|ProcessLiveness|Actor|Codex|Status' -count=1
GOCACHE=/tmp/agentflow-stage2-gocache go test -race ./internal/workflow ./provider/... ./internal/gitstate ./internal/engine -run 'ExecutionPolicy|ResourceBudget|RunLease|ProcessLiveness|Actor|Codex' -count=1
gopls check internal/workflow/execution_policy.go internal/engine/resource_budget.go provider/provider.go provider/codex/codex.go
git diff --check
```

The built-in Codex adapter intentionally rejects `costUSD`: the provider-neutral
contract supports monetary metering, but this adapter has no enforceable price
signal. A provider that reports and enforces cost may implement that limit.
