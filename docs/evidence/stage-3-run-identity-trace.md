# Stage 3 run identity and trace foundation

Date: 2026-09-01

This record closes the stable run/node identity, versioned trace, complete
orchestration-transition coverage, and provider/tool metadata targets of
Execution Stage 3. It does not claim completion of supervised sessions, status
detail, or `explain`.

## Implemented contract

- A v2 `run-identity` record contains an opaque 128-bit `run_id` alongside the
  SHA-256 workflow, resolved-parameter, and execution-environment digests.
- Compatible v1 digest-only records gain one run ID in place before recovery;
  incompatible inputs still fail before execution evidence is consumed.
- Each real phase attempt receives an opaque `node_execution_id` and a durable,
  monotonic per-phase `attempt`. Recovery reuses the active values rather than
  creating another execution identity.
- Pending provider authority records and provider metadata carry the run and
  node-execution identities, preserving attribution across crash reconciliation.
- Execution traces use schema version 1 and are append-only JSON Lines files at
  `.git/agentflow/traces/<run_id>.jsonl`. Each event contains a monotonic
  sequence, UTC timestamp, run ID, event kind, optional node identity/attempt,
  and an allowlisted set of non-secret orchestration fields.
- Trace files are separate from workflow SDL, Git acceptance refs, and
  operational provider output. Opening an existing trace validates its version,
  run binding, sequence continuity, and required event fields before append.
- `status` reports the run ID, trace schema/path, and active node identity and
  attempt without requiring original parameter or secret values.
- Attempt start, resume, recoverable block, skip, and finish events retain one
  node-execution identity. Parallel siblings remain independently attributed.
- Durable actor completion, engine-owned progress and bookkeeping changes, and
  work-item publication are recorded as state transitions after persistence.
- Validation events distinguish fresh success, reused evidence, classified
  failure, repaired success, and durable repair-budget exhaustion. Repair actor
  attempts record their ordinal and configured bound without failure output.
- Successful checkpoints record the resulting Git commit and whether they
  created it. Phase acceptance, human-gate evidence, and workflow completion
  identify the exact Git-backed record and commit.
- Recovery traces pending actor reconciliation and re-emits reconciled or
  reused durable evidence when interruption may have occurred between a Git
  state write and its trace append.
- Provider request metadata records only the enforced adapter and policy shape,
  counts, capture intent, and budgets. Static model identity is opaque; dynamic
  model values are not hashed. Provider responses contribute duration,
  structured token/cost usage, final-message presence, and classified outcome.
- Tool events record the authored type and mutation/capture declarations,
  duration, outcome, conditional skip, and shell exit code. Commands, expanded
  inputs, paths, regexes, and output are excluded.
- Prompts, objectives, reasoning configuration, private model reasoning,
  credentials, final-message content, provider output, and tool output are not
  required or persisted by this metadata contract.

## Verification

- `internal/executiontrace/trace_test.go` proves monotonic append across reopen
  and fail-closed rejection of an incompatible trace schema.
- `internal/engine/run_identity_test.go` interrupts a provider, resumes through
  a new engine, verifies stable run/node identities in provider metadata and
  status, and validates every persisted trace event's version and sequence. It
  also covers successful lifecycle/acceptance evidence, repair success and
  exhaustion, checkpoint commit evidence, conditional skips, and
  human/completion evidence under the race detector.
- `internal/engine/trace_metadata_test.go` covers provider request/response
  metering and policy metadata, dynamic-model privacy, tool metadata and exit
  codes, and absence of credentials, prompts, reasoning, final messages, model
  values, and command output from the JSONL trace.
- Existing run-identity tests continue to prove that plaintext parameters,
  model selections, and environment inputs are not persisted.
- The full engine suite completed in 500.6 seconds with only the previously known
  dirty-initialization diagnostic assertion mismatch; all identity, recovery,
  pending-invocation, and scheduler compatibility tests otherwise passed. The
  final parallel trace-attribution regression also passed under the race
  detector after the attribution-scope correction.

## Remaining Stage 3 work

- Add automation-oriented status detail and `agentflow explain`.
- Add exclusive supervised foreground/detached session handoff and attach.
