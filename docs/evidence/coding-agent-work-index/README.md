# Coding-agent work index

`v1.json` is a small, versioned, repository-local index of representative
coding-agent work used to build AgentFlow. It is an evidence directory index,
not a telemetry backend and not a source of workflow semantics.

Each entry represents coding-agent work that built AgentFlow. It has stable
repository-relative evidence, one task class, a bounded outcome statement, a
compact provider-independent trace, and the same three capture metrics: token
count, tool calls, and review time. Traces are either `captured` with a
digest-identified source ledger, ordered action summaries, and sorted tool
categories, or `unavailable` with a reason.

A metric is always marked `exact`, `derived`, `unavailable`, or
`not_applicable`. Provenance references evidence IDs from the same entry.
Exact values require a direct measurement description; derived values require
a reproducible calculation; unavailable and not-applicable values require a
reason and omit `value`. Token and tool-call values are nonnegative integers
bounded by the schema at the unsigned 64-bit maximum, while review time is a
nonnegative number of minutes. Derived counts use checked integer summation;
derived review time uses an elapsed-time calculation. The current source
ledgers do not prove token or tool-call totals, so those values remain
`unavailable` rather than estimated.

For these captured tasks, `review_time` is the elapsed correction-cycle
window from initial review registration through final approval, not active
human or model labor time. Its structured calculation records both RFC 3339
timestamps and the rounding precision, and the validator recomputes the value.

## Read and query

Validate the checked-in index from the repository root:

```sh
go test ./internal/corpusindex
```

The data is ordinary JSON and is provider-independent. For example:

```sh
go run ./cmd/jq -r '.entries[] | [.id, .task_class, .outcome.status] | @tsv' docs/evidence/coding-agent-work-index/v1.json
go run ./cmd/jq -r '.failure_modes[] | [.id, .title] | @tsv' docs/evidence/coding-agent-work-index/v1.json
```

## Add a future capture

1. Add a sorted entry to `v1.json` with stable repository-relative evidence.
   Prefer a checked-in test, evidence document, example, or implementation
   file. Do not make an ephemeral run directory the sole evidence source.
2. Capture only allowlisted trace content: ordered phases, short action
   summaries, provider-independent tool categories, a source-ledger digest,
   and the bounded source sequence. Do not copy raw event payloads.
3. Record only an exact provider/tool/review measurement that the referenced
   evidence proves. Use `derived` only with a reproducible calculation and
   source evidence; otherwise use `unavailable` or `not_applicable`.
4. Link each failure mode to task IDs where it was observed. Its frequency is
   the count observed in this curated sample, not a claim about project-wide
   prevalence.
5. Update `v1.schema.json` and `internal/corpusindex/index.go` together for a
   schema change, then extend the malformed-index cases in the test.
6. Keep entries, evidence IDs, failure modes, and tool categories in
   lexicographic order, keep trace action order contiguous from one, and
   run the validation command above.

The JSON Schema encodes field shape, nonempty collections, enums and constants,
fixed metric membership and order, value types, and status/value/provenance
conditionals. The Go validator additionally performs cross-reference,
lexicographic and contiguous-order checks, privacy-signature scanning, global
uniqueness, representative-class coverage, and repository-confined file
resolution. Tests mechanically compare schema enums, metric order/cardinality,
and conditional coverage with those Go rules.

## Limitations and privacy

This index is a curated representative sample, not a complete activity log.
It does not infer quality from a test's existence and it does not compare
providers, models, or people. It must never include raw prompts, private model
reasoning, credentials, transcripts, command text or output, tool or provider
payloads, final-message content, or ephemeral process identities. Automated
checks reject unsafe key variants, established credential and private-key
signatures, and repository paths that escape through traversal or symlinks.
Because arbitrary prose can still disclose material that signatures cannot
reliably recognize, every new or edited title, summary, description,
calculation, reason, and trace action requires human privacy review.
