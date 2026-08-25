# Reference

Reference documents are the source and implementation references used to
understand AgentFlow precisely.

- [AgentWorkflow v1alpha1 field guide](agentflow-v1alpha1.md) is the normative
  field-level and semantic reference for the workflow document. It describes
  the declarative contract and its operational invariants.
- [AgentWorkflow v1alpha2 authoring contract](agentflow-v1alpha2.md) defines the
  concise authoring form and dependency/acceptance semantics. Its core is
  strictly decoded and normalized, but it remains unsupported for execution.
- [Go interpreter](runtime.md) is the implementation/runtime reference. It
  documents the currently executable surface, Git-backed state, provider
  behavior, CLI, and explicit runtime limits.

The field guide may describe a broader v1alpha1 contract than the current
interpreter supports. Use the runtime reference to distinguish documented
semantics from what can execute today.
