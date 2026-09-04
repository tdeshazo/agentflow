# Evidence

Evidence records completed execution, verification, and closeout claims. They
point to durable artifacts, commits, checks, or recovery results that support a
claim about what happened.

Evidence is not normative specification text. It should not be used as the
authority for field semantics or runtime behavior; use the
[reference](../reference/README.md) documents for those questions.

Current closure records:

- [Coding-agent work index](coding-agent-work-index/README.md) records a
  privacy-safe, evidence-backed representative sample of AgentFlow build work
  and its available metrics.

- [Stage 7 executor and tool extensibility](stage-7-executor-tool-extensibility.md)
  records versioned provider/tool contracts, capability negotiation, typed
  deterministic plugins, and fail-closed boundaries.

- [Stage 3 run identity and trace foundation](stage-3-run-identity-trace.md)
  records stable run/node-execution identities, recovery behavior, and the
  separate versioned execution-trace contract.
- [Stage 2 runtime security](stage-2-runtime-security.md) records execution
  ownership, policy, credentials, approval, and resource-budget closure.
- [Canonical self-hosting migration](canonical-self-hosting-migration.md)
  proves the equal-or-stronger v1alpha4 authority and retained v1alpha1
  compatibility boundary.
- [Foundation closure](foundation-closure.md) records the executable v1alpha1
  foundation and successor-runtime baseline.
