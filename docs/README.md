# AgentFlow documentation

This is the purpose-based documentation front door for AgentFlow. Start with
the section that matches the question you are trying to answer:

- [Architecture](architecture/): durable design contracts and authority
  boundaries.
- [Guides](guides/): contributor workflows, concise workflow authoring, and
  development checks.
- [Planning](planning/): how to navigate and execute the canonical roadmap.
- [Reference](reference/): normative AgentWorkflow field semantics and the Go
  runtime reference.
- [Research](research/README.md): exploratory findings that inform design and
  planning.
- [Evidence](evidence/README.md): retained proof of completed execution and
  verification claims.
- [Reviews](reviews/README.md): dated project assessments.

## Where to start

- To understand who may act and who may accept work, read
  [Execution authority](architecture/execution-authority.md).
- To author or audit a workflow with typed handoffs or typed work items, start
  with the [AgentWorkflow v1alpha4 work-item authoring contract](reference/agentflow-v1alpha4.md).
  The repository-scale example is the
  [canonical v1alpha4 self-hosting workflow](../spec/agent-workflow.yaml), with
  its authority proof retained in
  [migration evidence](evidence/canonical-self-hosting-migration.md).
  The [AgentWorkflow v1alpha3 authoring contract](reference/agentflow-v1alpha3.md)
  remains the typed-handoff contract.
  The [AgentWorkflow v1alpha2 authoring contract](reference/agentflow-v1alpha2.md)
  remains the concise sequential contract. The
  [AgentWorkflow v1alpha1 field guide](reference/agentflow-v1alpha1.md) and
  [concise authoring guide](guides/concise-authoring.md) remain the
  compatibility references.
- To assess a legacy workflow before migration, read the
  [v1alpha1 capability matrix](reference/v1alpha1-capability-matrix.md) and
  run `agentflow migrate --check -f workflow.yaml`.
- To understand the current interpreter, read the
  [Go runtime reference](reference/runtime.md).
- To contribute a change, follow the
  [development guide](guides/development.md).
- To choose roadmap work, use the
  [planning index](planning/README.md), which points to the root
  [ROADMAP.md](../ROADMAP.md) as the canonical source of priority and status.

The root [README.md](../README.md) remains the repository and product front
door. This index is the main entry point for the documentation itself.
