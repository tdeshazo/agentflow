# AgentFlow Workflow Specification

AgentFlow is a small, declarative specification for coordinating AI agents,
deterministic tools, workspace mutation, validation, recovery, human
verification, and completion.

This repository contains:

- a reference `agentflow.dev/v1alpha1` `AgentWorkflow` definition;
- a field-level specification reference;
- an agent skill for authoring, describing, reviewing, and comparing workflow
  specifications without requiring AgentFlow implementation source;
- an executable workflow example;
- an experimental Go interpreter with a provider-neutral execution interface;
  and
- a roadmap for evolving AgentFlow into a portable YAML SDL with a reference
  Go interpreter.

## Start here

[docs/README.md](docs/README.md) is the main documentation entry point. It
organizes the documentation by purpose:

- [Execution authority](docs/architecture/execution-authority.md) explains
  the current separation among definition, execution, and assurance.
- [Development guide](docs/guides/development.md) covers prerequisites,
  deterministic checks, focused commands, and contributor workflow.
- [AgentWorkflow v1alpha1 reference](docs/reference/agentflow-v1alpha1.md)
  documents field semantics.
- [Go runtime reference](docs/reference/runtime.md) documents the current
  interpreter and its limits.
- [Planning](docs/planning/README.md) navigates the canonical root
  [ROADMAP.md](ROADMAP.md).
- [Research](docs/research/README.md), [evidence](docs/evidence/README.md),
  and [reviews](docs/reviews/README.md) explain the repository's exploratory,
  proof, and assessment records.

## Repository layout

```text
.
├── README.md
├── ROADMAP.md
├── CHANGELOG.md
├── .gitignore
├── go.mod
├── go.sum
├── main.go
├── provider/
│   ├── provider.go
│   └── codex/
├── internal/
│   ├── agentflowcli/
│   ├── engine/
│   ├── gitstate/
│   └── workflow/
├── scripts/
│   └── check.sh
├── spec/
│   └── agent-workflow-v1alpha1.yaml
├── docs/
│   ├── architecture/
│   ├── evidence/
│   ├── guides/
│   ├── planning/
│   ├── reference/
│   ├── research/
│   └── reviews/
├── skills/
│   └── agentflow-spec/
└── examples/
    └── art-portfolio.agent-workflow.yaml
```

## Install

Install the CLI from the module root:

```sh
go install github.com/tdeshazo/agentflow@latest
```

Then run `agentflow` with a workflow command, for example
`agentflow validate -f workflow.yaml`.

## What the specification models

The format separates orchestration authority into explicit domains:

- **Agents** perform bounded reasoning and workspace work.
- **Deterministic tools** produce authoritative checks and repository
  operations.
- **Workspace policy** defines what may change and what must remain protected.
- **Validation** decides whether a phase may advance and how repair is bounded.
- **State and recovery** make interrupted workflows resumable without
  discarding useful work.
- **Human gates** represent manual verification as durable workflow state.
- **Completion** is an explicit final transition after all required assertions
  and gates pass.

A central invariant is that an agent may mutate the workspace, but it does not
decide whether its own phase is accepted. Advancement belongs to deterministic
workflow logic.

## Current interpreter

The experimental Go interpreter executes the supported core of `v1alpha1`
against Git workspaces, stores durable workflow state in namespaced Git refs,
and uses the public [provider interface](provider/provider.go) for AI
execution. The initial adapter uses non-interactive Codex CLI execution.

See the [runtime reference](docs/reference/runtime.md) for CLI usage, state
layout, supported constructs, provider behavior, and current limits. The
[art portfolio workflow](examples/art-portfolio.agent-workflow.yaml) creates
a FastAPI backend, React frontend, and containerized deployment.

## Validation

Run the repository-owned deterministic development gate:

```sh
./scripts/check.sh
```

It checks formatting and diff hygiene, tests, vet, race-enabled tests, and the
reference workflow definition. It does not make live model calls. For
individual workflow validation, see the
[development guide](docs/guides/development.md).

## Agent skill

The [AgentFlow specification skill](skills/agentflow-spec/SKILL.md)
teaches an agent how to create and modify executable workflows from the bundled
public contract, then validate and inspect their expanded plans without reading
AgentFlow implementation source. It also explains, reviews, and compares
existing workflows while preserving the separation among agent authority,
workspace mutation authority, deterministic validation authority, human gates,
and completion.

## Status

The current specification identifier is:

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
```

`v1alpha1` is experimental. Field names and semantics may evolve before a
stable release.

## Publishing

This package intentionally does **not** select a software license for you.
Before publishing publicly, add the license that matches how you want others
to use the specification and skill.
