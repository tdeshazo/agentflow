# Contributing

Contributions should preserve the specification's separation between agent behavior and workflow authority.

When changing the specification:

1. Update the applicable versioned reference under `docs/reference/` when field semantics change.
2. Keep the canonical `spec/agent-workflow.yaml` self-hosting workflow valid and retain `spec/agent-workflow-v1alpha1.yaml` as an executable compatibility definition.
3. Update the public authoring skill if a new concept changes how workflows should be authored, explained, or audited.
4. Add or update an example when a semantic change is difficult to understand from the reference definition alone.
5. Keep deterministic advancement, mutation boundaries, recovery behavior, human gates, and completion semantics explicit rather than burying them in agent prompts.

Before submitting changes, verify that all YAML files parse successfully and that relative links in the Markdown files remain valid.

Run `scripts/check.sh` before submitting. It is the canonical repository gate
for formatting, Go tests, vet, race coverage, and shipped workflow validation.
