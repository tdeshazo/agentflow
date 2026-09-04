# Stage 7 executor and tool extensibility

Date: 2026-09-03

Stage 7 closes the executor/tool extensibility criteria without broadening
workflow authority.

- `provider.Contract` is `agentflow.dev/provider/v1`; it describes portable
  execution modes, invocation-context support, filesystem isolation, and
  execution-policy enforcement. The shared conformance harness invokes both
  reference adapters across success, error, and cancellation paths and checks
  that enforcement metadata agrees with implemented enforcement interfaces.
- Successor APIs expose `agent.requirements`; grammar-frozen v1alpha1 rejects
  that field. Engine construction and run startup preflight every selected
  actor provider's mandatory enforcement interfaces before durable state,
  worktree creation, provider invocation, or workspace mutation. The versioned
  contract is required only when requirements are explicit.
- `tool.Registry` is explicit and has no init-time registration. Its v1 typed
  plugins decode configuration strictly and declare `none` or `workspace`
  mutation. A `MutationNone` call is enclosed by a whole authoritative-
  worktree snapshot and any delta is rejected. Cacheable plugins provide an
  immutable behavior fingerprint included with the complete descriptor and
  config in validation evidence identity; plugins without one execute without
  reusable evidence.
- Typed plugin `config` is successor-only authoring; frozen v1alpha1 rejects
  it. Exported standalone tool execution establishes normal runtime lineage and
  applies the same pre/post mutation boundary as validation tools.
- `internal/engine/extensions_test.go` proves an unsatisfied executor contract,
  malformed plugin config, and undeclared mutation leave the repository clean;
  it also runs a registered typed deterministic tool without changing the core
  dispatcher. The public `runtime` package provides explicit provider and tool
  registry injection for embedders. CLI plugin discovery is not implemented.

The local-command adapter is intentionally not an actor sandbox: its contract
does not claim filesystem or execution-policy enforcement. Human and nested
workflow modes remain negotiateable but unsupported until adapters can provide
their required enforcement.
