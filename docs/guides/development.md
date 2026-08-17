# Development guide

This guide covers the normal contributor workflow. Field semantics belong in
the [AgentWorkflow v1alpha1 reference](../reference/agentflow-v1alpha1.md);
runtime behavior belongs in the [Go runtime reference](../reference/runtime.md).

## Prerequisites

From the repository root, install or provide:

- Go 1.24.0, as declared by `go.mod`;
- Git;
- Bash and the repository tools used by `scripts/check.sh`, including
  `rg`, `gofmt`, and standard POSIX utilities.

A Codex CLI installation is also required to run the provider-backed
self-hosting workflow. Deterministic checks do not make live model calls.

## Canonical deterministic gate

Run the repository-owned gate from the repository root:

```sh
./scripts/check.sh
```

It checks Go formatting and diff hygiene, tests, vet, race-enabled tests, the
deterministic self-hosting runtime tests, and validation of every shipped
workflow definition. Treat it as the canonical development gate; CI invokes the
same script.

## Focused commands

Use focused checks while iterating:

```sh
gofmt -w path/to/file.go
go test ./internal/engine -run TestName -count=1
go test ./internal/workflow -run TestName -count=1
go test ./...
go test -race ./...
go vet ./...
```

Validate a workflow without opening a repository or invoking a provider:

```sh
go run ./cmd/agentflow validate -f examples/develop-agentflow.agent-workflow.yaml
```

The validator distinguishes invalid documents from documents that are valid but
unsupported by this runtime. The shipped definitions can all be checked with
`./scripts/check.sh`.

## Bounded self-development

Use the repository-owned workflow for one specific task, starting from a clean
named branch:

```sh
go run ./cmd/agentflow run \
  -f examples/develop-agentflow.agent-workflow.yaml \
  -C . \
  --set "task=Add focused regression coverage for <bounded behavior>."
```

Inspect the resulting checkout at the human gate and type `yes` when the
requested verification is complete. Use `status` (or `status --json`) to
inspect durable state, and rerun the same command with the same task to resume
after an interruption. Use `reset` only to intentionally abandon that run's
orchestration history.

## Failure diagnosis

- A validation error before execution usually means malformed YAML, an unknown
  reference, or an unsupported runtime construct. Run the `validate` command
  directly and read its YAML path/source diagnostic.
- A clean-workspace or branch error means the workflow's initialization,
  phase, or reset precondition is not satisfied. Inspect `git status` and
  `git branch --show-current`; handle unrelated changes explicitly.
- A validation failure is recoverable only within its declared repair budget.
  Check `status --json` for the failed validation and rerun the same bounded
  task so the runtime can resume the active phase.
- A safety, integrity, protected-file, or out-of-scope mutation failure is
  terminal by design. Revert or isolate the offending change, then reset the
  workflow state if starting over is intentional.
- A changed task, model, environment input, or executable workflow definition
  invalidates the saved run identity. Use the exact original inputs to resume,
  or explicitly reset before starting a different task.
- A stale phase/completion marker or branch-lineage error means its recorded
  commit is no longer valid for the current checkout. Restore the expected
  branch/ancestry or reset the run deliberately.

For runtime and field-level semantics, use the categorized reference documents
rather than expanding this workflow guide into a second specification.
