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

A Codex CLI installation is also required to run provider-backed workflows.
Deterministic checks do not make live model calls.

## Canonical deterministic gate

Run the repository-owned gate from the repository root:

```sh
./scripts/check.sh
```

It checks Go formatting and diff hygiene, tests, vet, race-enabled tests, and
every checked-in executable workflow definition under `spec/` and `examples/`.
Treat it as the canonical development gate; CI invokes the same script.

## Focused commands

Use focused checks while iterating:

```sh
gofmt -w path/to/file.go
go test ./internal/engine -run TestName -count=1
go test ./internal/workflow -run TestName -count=1
go test ...
go test -race ...
go vet ...
```

Validate a workflow without opening a repository or invoking a provider:

```sh
go run . validate -f workflow.yaml
go run . plan --expanded -f workflow.yaml
```

The validator distinguishes invalid documents from documents that are valid but
unsupported by this runtime. The concise v1alpha2 form and successor v1alpha3/
v1alpha4 forms are executable when they use the supported `codex` actors and
shell validations; `plan --expanded`
shows the normalized workspace authority, named actors, repair budget,
dependency graph, acceptance boundary, and final completion validation. The
shipped definitions can all be checked with `./scripts/check.sh`.

For the checked-in v1alpha2 example:

```sh
go run . validate -f internal/workflow/testdata/conformance/valid/v1alpha2-concise.yaml
go run . plan --expanded -f examples/feature.agent-workflow.yaml
```

Use the expanded plan when reviewing concise authoring defaults. It shows the
normalized executable lifecycle and safety/repair/completion contract without
calling an actor or a mutable tool.

## Selecting a repository workflow

For repeated work in one repository worktree, select a discovered workflow by
logical name instead of repeating `-f`:

```sh
go run . switch feature -C /path/to/repository
go run . current -C /path/to/repository
go run . workflows -C /path/to/repository
go run . validate -C /path/to/repository
go run . logs -C /path/to/repository
```

`switch` with no name opens the usual terminal picker; use `switch -` to swap
to the previous selection, `switch --clear` to return to the legacy
no-selection path, or `checkout` as a compatibility alias for `switch`.
Discovery is deterministic: repository workflows under
`.agentflow/workflows/` shadow names from `~/.agentflow/workflows/`, and
`workflows` marks the active name with `*`.

The selector is local Git worktree metadata, not workflow state. Different
linked worktrees can therefore select different workflows. Selector precedence
is explicit command-line selector, repository config, home config, then active
selection; `status --all` ignores it. A deleted or unavailable active workflow
is reported as stale by commands that resolve it. Use `current` to inspect the
stored name, then switch to an available name or clear it; AgentFlow never
silently substitutes another workflow.

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
