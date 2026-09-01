# Stage 3 run identity and trace foundation

Date: 2026-09-01

This record closes the stable run/node identity and versioned trace targets of
Execution Stage 3. It does not claim completion of supervised sessions,
`explain`, or the full transition vocabulary.

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

## Verification

- `internal/executiontrace/trace_test.go` proves monotonic append across reopen
  and fail-closed rejection of an incompatible trace schema.
- `internal/engine/run_identity_test.go` interrupts a provider, resumes through
  a new engine, verifies stable run/node identities in provider metadata and
  status, and validates every persisted trace event's version and sequence.
- Existing run-identity tests continue to prove that plaintext parameters,
  model selections, and environment inputs are not persisted.
- The full engine suite completed in 479 seconds with only the previously known
  dirty-initialization diagnostic assertion mismatch; all identity, recovery,
  pending-invocation, and scheduler compatibility tests otherwise passed. The
  final parallel trace-attribution regression also passed under the race
  detector after the attribution-scope correction.

## Remaining Stage 3 work

- Broaden trace events to fully explain every blocked, repaired, gated, skipped,
  and failed transition and connect them to Git completion evidence.
- Add automation-oriented status detail and `agentflow explain`.
- Add exclusive supervised foreground/detached session handoff and attach.
