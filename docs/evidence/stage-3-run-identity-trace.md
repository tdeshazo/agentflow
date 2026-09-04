# Stage 3 run identity and trace foundation

Date: 2026-09-01

This record closes Execution Stage 3: stable run/node identity, versioned
traces, complete orchestration-transition coverage, provider/tool metadata,
durable explanations, and supervised terminal handoff.

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
- `internal/executiontrace/trace_test.go`, `internal/engine/status_test.go`, and
  `internal/agentflowcli/main_test.go` prove that `status --detail` returns a
  bounded chronological event window in both human and JSON forms, reports
  trace availability without hiding authoritative status, and leaves torn
  diagnostic tails untouched.
- The full engine suite completed in 500.6 seconds with only the previously known
  dirty-initialization diagnostic assertion mismatch; all identity, recovery,
  pending-invocation, and scheduler compatibility tests otherwise passed. The
  final parallel trace-attribution regression also passed under the race
  detector after the attribution-scope correction.

## Supervised session and explain closure (2026-09-03)

- `engine.Explain` classifies a requested phase from Git-backed active records,
  deterministic acceptance markers, and declared dependency markers. A bounded
  trace read contributes only the runtime's fixed skip vocabulary; it cannot
  authorize or contradict the durable result.
- `agentflow run --detach` launches a child before workflow execution, and the
  launcher waits on a bounded inherited readiness pipe until the child owns its
  lease, durable run identity, and verified supervision state. Startup errors
  and timeouts fail the launch; IPC-unavailable fallback is reported explicitly.
- An ordinary interactive `agentflow run` uses the same readiness path and
  holds the child before workflow execution until it becomes the child's
  exclusive authenticated attachment and acknowledges that reservation over a
  private return pipe. Terminal EOF sends
  the detach protocol message, so the already-running child retains its lease,
  run ID, and in-flight work without restarting or replaying an actor.
- Unix readiness uses inherited descriptors; Windows command construction uses
  `AdditionalInheritedHandles`, never `Cmd.ExtraFiles`. Private output files
  use directory-relative no-follow opens plus owner checks on supported Unix
  hosts, and fail closed where those guarantees cannot be established.
  `agentflow attach` verifies the run ID, descriptor process start token, and
  private session metadata before claiming the one attachment slot.
- Session metadata and captured output remain under the Git-aware private
  runtime directory. Unix-domain endpoints use a short, owner-private
  per-user directory under the system temporary directory and a fixed-length
  opaque name; session metadata and the authenticated protocol retain the
  repository, workflow, run, and process identity binding. Private directories
  use `0700` permissions and files use `0600`. Unverifiable, malformed, stale,
  mismatched, or concurrently attached sessions fail closed. If a host forbids
  private local IPC, detached execution remains lease-protected but is
  deliberately unavailable to `attach`; no weaker transport is substituted.
- Attach replays a per-run rotating output window, then consumes ordered live
  frames through the supervisor connection. Live frames remain available when
  diagnostic persistence reaches capacity. After output producers drain, the
  supervisor sends an authoritative final cursor/completion frame; attach does
  not infer successful completion from socket EOF. EOF from the terminal is an
  explicit detach that leaves the same process, lease, run ID, and work active.
  Input uses gate-scoped generations so disabling one gate invalidates pending
  writes and cannot feed a later gate. Input and terminal control messages are
  not logged or added to the orchestration trace.
- Explain verifies the durable workflow-definition digest before classifying
  state, without resolving secret values, and retains only safe actor/provider
  kind and stage for serial and parallel provider failures.
- `internal/engine/explain_test.go`, `internal/supervision/session_test.go`,
  `internal/observability/logs_test.go`, and CLI tests cover durable explain
  classifications, stale/mismatched identity rejection, exclusive attachment,
  generation-scoped human input, interruption forwarding, cross-run replay
  isolation, startup readiness, terminal drain, diagnostic-capacity behavior,
  slow-client queue saturation, concurrent cursor ordering, and symlink
  rejection. Unix-domain listener integration is skipped only when the host
  sandbox forbids local sockets; attachment rejects unavailable session IPC
  rather than silently creating an unsafe transport.

Stage 3 is complete: run/node identity, a versioned non-authoritative trace,
durable explainability, and supervised terminal handoff are all implemented.
