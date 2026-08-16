# AgentFlow Workflow Specification

A small, declarative specification for coordinating AI agents, deterministic tools, workspace mutation, validation, recovery, human verification, and completion.

This repository contains:

- a reference `agentflow.dev/v1alpha1` `AgentWorkflow` definition;
- a field-level specification reference;
- an agent skill for efficiently describing, reviewing, and comparing workflow specifications;
- a concrete Priority 5 workflow example translated from an imperative shell orchestrator;
- an experimental Go interpreter with a provider-neutral execution interface and an initial Codex CLI provider; and
- a roadmap for evolving AgentFlow into a portable YAML SDL with a reference Go interpreter.

## Repository layout

```text
.
├── README.md
├── ROADMAP.md
├── CHANGELOG.md
├── .gitignore
├── go.mod
├── go.sum
├── cmd/agentflow/
├── provider/
│   ├── provider.go
│   └── codex/
├── internal/
│   ├── engine/
│   ├── gitstate/
│   └── workflow/
├── spec/
│   └── agent-workflow-v1alpha1.yaml
├── docs/
│   ├── agentflow-v1alpha1.md
│   ├── runtime.md
│   └── research/
├── skills/
│   └── agent-workflow-spec-describer/
│       ├── SKILL.md
│       └── references/
│           └── agentflow-v1alpha1.md
└── examples/
    └── finish-priority-05.agent-workflow.yaml
```

## What the specification models

The format separates orchestration authority into explicit domains:

- **Agents** perform bounded reasoning and workspace work.
- **Deterministic tools** produce authoritative checks and repository operations.
- **Workspace policy** defines what may change and what must remain protected.
- **Validation** decides whether a phase may advance and how repair is bounded.
- **State and recovery** make interrupted workflows resumable without discarding useful work.
- **Human gates** represent manual verification as durable workflow state.
- **Completion** is an explicit final transition after all required assertions and gates pass.

A central invariant is that an agent may mutate the workspace, but it does not decide whether its own phase is accepted. Advancement belongs to deterministic workflow logic.

## Start here

Read [`docs/agentflow-v1alpha1.md`](docs/agentflow-v1alpha1.md) for the field semantics, then inspect [`spec/agent-workflow-v1alpha1.yaml`](spec/agent-workflow-v1alpha1.yaml) for a complete reference definition.

See [`ROADMAP.md`](ROADMAP.md) for the planned progression from the current `v1alpha1` foundation to a GitHub-Actions-like YAML authoring model, broader Go runtime support, explicit DAG execution, typed artifacts, extensibility, security, observability, composition, and `v1beta1` conformance.

The project's minimum viable product is **self-hosting**: AgentFlow plus its Go interpreter must be capable of orchestrating a real, validated, resumable change to `agentflow-spec` itself without a bespoke shell script owning phase advancement, repair, checkpointing, or completion.

## Go interpreter

The repository now includes an experimental interpreter for the executable core of `v1alpha1`. It keeps durable orchestration state in Git objects and namespaced refs rather than a separate database, and exposes AI execution through the public [`provider.Provider`](provider/provider.go) interface. The initial adapter uses non-interactive Codex CLI execution.

```sh
go run ./cmd/agentflow run \
  -f examples/finish-priority-05.agent-workflow.yaml \
  -C /path/to/target/repository

go run ./cmd/agentflow status -f workflow.yaml -C /path/to/repo
go run ./cmd/agentflow reset  -f workflow.yaml -C /path/to/repo
go run ./cmd/agentflow validate -f workflow.yaml
```

See [`docs/runtime.md`](docs/runtime.md) for state layout, supported constructs, provider behavior, and current limits.

The [`examples/finish-priority-05.agent-workflow.yaml`](examples/finish-priority-05.agent-workflow.yaml) file demonstrates a concrete workflow with:

- nine ordered AI phases;
- per-phase model and reasoning assignments;
- protected Git/workspace boundaries;
- a canonical quality gate;
- one bounded repair attempt for phase failures;
- commit-aware checkpointing and resume;
- manual SSH/terminal verification; and
- deterministic completion bookkeeping.

## Agent skill

The skill at [`skills/agent-workflow-spec-describer/SKILL.md`](skills/agent-workflow-spec-describer/SKILL.md) teaches an agent how to explain the specification efficiently.

It prioritizes execution semantics over repeating prompts and distinguishes:

1. agent authority;
2. workspace mutation authority; and
3. deterministic validation authority.

The skill supports compact descriptions, detailed audits, and semantic workflow comparisons.

## Status

The current specification identifier is:

```yaml
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
```

`v1alpha1` should be treated as an experimental format. Field names and semantics may evolve before a stable release.

## Validation

`agentflow validate -f workflow.yaml` decodes the authoritative executable
model, checks structural types and document references, and reports whether a
workflow is invalid, executable, or spec-valid but unsupported by this runtime.
It does not create workspace or Git state. The Go interpreter fails closed on
unsupported executable constructs; the field guide remains broader than the
current runtime.

## Publishing

This package intentionally does **not** select a software license for you. Before publishing publicly, add the license that matches how you want others to use the specification and skill.
