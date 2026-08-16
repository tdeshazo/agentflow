# Agent Workflow and Orchestration Research Landscape

**Research snapshot:** 2026-08-15

This note summarizes leading research and engineering patterns for agent workflows and agent orchestration, with an emphasis on implications for a declarative workflow specification such as AgentFlow.

The central trend is a shift away from treating the model prompt as the whole agent system. The more useful unit of analysis is the **agentic execution system**: models, deterministic tools, state, permissions, workflow control, verification, recovery, and human intervention composed into an execution graph.

A useful way to frame the literature is to separate three things:

1. **Workflow definition** — the reusable control structure, policies, actors, and success conditions.
2. **Execution graph** — the concrete nodes and dependencies instantiated for one run.
3. **Execution trace** — the observed sequence of actions, tool calls, state transitions, failures, and recoveries.

This distinction is increasingly explicit in workflow-optimization research and maps naturally to a declarative orchestration format.

## 1. Reason, act, observe: the foundational agent loop

### ReAct

ReAct established the now-standard pattern of interleaving model reasoning with actions against an external environment. Instead of asking a model to produce a full answer in one pass, the agent repeatedly reasons, acts, receives an observation, and updates its next action.

This matters architecturally because an agent invocation is not merely `prompt -> response`. A workflow runtime may need to manage:

- multiple model turns;
- tool calls and observations;
- termination criteria;
- action budgets;
- tool permissions;
- failure handling; and
- durable state outside the model context.

**AgentFlow implication:** an agent phase should represent an execution capability with bounded authority, not a single completion string.

Reference: [ReAct: Synergizing Reasoning and Acting in Language Models](https://arxiv.org/abs/2210.03629)

## 2. Workflows and agents are different control mechanisms

Anthropic's production guidance distinguishes **workflows**, where code or configuration determines the control path, from **agents**, where the model dynamically determines its own next actions and tool use.

This is a useful distinction for an orchestration specification. Dynamic reasoning is valuable inside a bounded task, while important policy decisions often benefit from deterministic ownership.

Examples of deterministic workflow responsibility include:

- which phase executes next;
- whether a phase may mutate the workspace;
- which files or resources are protected;
- whether validation passed;
- how many retries are permitted;
- when human confirmation is required; and
- whether the overall workflow is complete.

**AgentFlow implication:** model autonomy and workflow authority should be separately expressible.

Reference: [Building effective agents](https://www.anthropic.com/engineering/building-effective-agents)

## 3. Deliberation can be search rather than a single chain

### Tree of Thoughts

Tree of Thoughts generalizes linear chain-of-thought reasoning into search over multiple candidate reasoning paths. Candidate states can be generated, evaluated, discarded, and revisited.

Reference: [Tree of Thoughts: Deliberate Problem Solving with Large Language Models](https://arxiv.org/abs/2305.10601)

### Language Agent Tree Search

Language Agent Tree Search extends search into interactive agent environments by combining reasoning, actions, environment feedback, reflection, and Monte Carlo Tree Search.

Reference: [Language Agent Tree Search Unifies Reasoning, Acting, and Planning in Language Models](https://arxiv.org/abs/2310.04406)

The orchestration lesson is not that every workflow should use tree search. It is that **control strategy is a configurable property of execution**. Useful workflow primitives can include:

- sequential execution;
- conditional branches;
- bounded loops;
- parallel fan-out/fan-in;
- retries;
- fallback paths; and
- optional search strategies inside a node.

**AgentFlow implication:** avoid baking total serialization into the core schema. Dependencies should describe what must precede what, leaving independent work eligible for parallel execution.

## 4. Reflection helps, but external verification is stronger

### Reflexion

Reflexion showed that an agent can improve over repeated attempts by retaining linguistic feedback about prior failures, without updating model weights.

Reference: [Reflexion: Language Agents with Verbal Reinforcement Learning](https://arxiv.org/abs/2303.11366)

Reflection is useful as a repair strategy, but self-assessment is not a reliable substitute for checking the environment.

### Long-horizon harnesses and independent audit

LongHorizon-Harness moves much of the reliability burden outside the acting model. Its Manage-Execute-Audit structure maintains explicit task state, gives an executor a bounded unit of work, and uses an independent read-only audit step to verify environment facts before advancement.

This supports a strong orchestration principle:

> Agents may perform work and propose completion; deterministic or independently verified conditions decide whether the work counts as complete.

Examples include tests, type checks, repository predicates, resource-state queries, invariant checks, or read-only audits.

**AgentFlow implication:** completion claims from an agent should be evidence, not authority. Validation should be modeled independently from execution.

Reference: [LongHorizon-Harness](https://arxiv.org/abs/2608.01964)

## 5. Structured roles and SOPs reduce coordination ambiguity

### MetaGPT

MetaGPT uses specialized roles and Standard Operating Procedures to structure software-development tasks. Its key contribution to orchestration is less the particular personas than the explicit intermediate artifacts and phase boundaries between them.

Reference: [MetaGPT: Meta Programming for A Multi-Agent Collaborative Framework](https://arxiv.org/abs/2308.00352)

### ChatDev

ChatDev structures software creation into role-specialized phases such as design, implementation, and testing.

Reference: [ChatDev: Communicative Agents for Software Development](https://arxiv.org/abs/2307.07924)

### AutoGen

AutoGen generalized multi-agent conversations into programmable patterns involving models, tools, and humans.

Reference: [AutoGen: Enabling Next-Gen LLM Applications via Multi-Agent Conversation](https://arxiv.org/abs/2308.08155)

The common lesson is that specialized actors become substantially more useful when the system defines explicit contracts around them:

- required inputs;
- allowed tools;
- mutation permissions;
- expected artifacts;
- success predicates; and
- handoff semantics.

**AgentFlow implication:** define actor capabilities and node contracts structurally rather than depending on role-description prose alone.

## 6. Multi-agent systems are not automatically better

A growing body of work cautions against assuming that adding agents improves reliability.

### Multi-agent failure modes

A 2025 study of multi-agent systems identified recurring failures across specification and system design, inter-agent coordination, and verification/termination. Role prompts alone were not sufficient to eliminate these issues.

Reference: [Why Do Multi-Agent LLM Systems Fail?](https://arxiv.org/abs/2503.13657)

### Single-agent structured workflows as a strong baseline

A 2026 study found that a single agent following the same structured workflow could match homogeneous multi-agent configurations across multiple benchmarks, while obtaining efficiency benefits such as shared KV-cache reuse.

Reference: [Rethinking the Value of Multi-Agent Workflow](https://arxiv.org/html/2601.12307v1)

### Heterogeneous model collaboration

Research on heterogeneous orchestration suggests that multiple agents can be valuable when they genuinely differ in model capability, perspective, cost, permissions, or information. Coordination protocol matters: exposing authorship, votes, or intermediate consensus signals can introduce herding or premature convergence.

Reference: [Heterogeneous multi-agent orchestration study](https://arxiv.org/pdf/2509.23537)

A practical rule follows:

> Use multiple agents because they provide meaningfully different capabilities or independent evidence, not merely because several personas can be instantiated.

**AgentFlow implication:** the fundamental abstraction should be an executor or actor. That actor may be a model, deterministic tool, human, remote agent, or composite system.

## 7. Manager/worker orchestration is a recurring topology

Central coordinator plus bounded workers appears repeatedly in both research and production systems.

Anthropic's multi-agent research system uses a lead agent to decompose work and dispatch parallel subagents. Their engineering reports also emphasize durable execution so that long-running work can resume instead of restarting from scratch.

Reference: [How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)

OpenAI's agent orchestration model distinguishes manager-style patterns, where a central agent invokes specialists as tools, from handoffs where control is transferred to another specialist.

Reference: [OpenAI agent orchestration](https://developers.openai.com/api/docs/guides/agents/orchestration)

Research on evolving orchestration goes further by learning a centralized controller that dynamically chooses which workers to invoke and in what sequence.

Reference: [Multi-Agent Collaboration via Evolving Orchestration](https://arxiv.org/html/2505.19591)

The important separation for high-integrity workflows is:

- dynamic orchestration may decide **what work to attempt**;
- policy should separately decide **what effects are authorized**; and
- validation should decide **whether the result may advance the workflow**.

## 8. Explicit dependencies enable parallelism

### LLMCompiler

LLMCompiler treats tool execution as a dependency-scheduling problem. A planner builds a plan, a scheduler identifies ready work, and independent tasks can execute concurrently.

Reference: [LLMCompiler: An LLM Compiler for Parallel Function Calling](https://arxiv.org/abs/2312.04511)

The broader lesson is that dependencies should be explicit rather than inferred from list order.

A workflow node might therefore declare:

```yaml
needs:
  - phase-a
  - phase-b
```

The runtime can then safely run unrelated ready nodes in parallel.

**AgentFlow implication:** model a DAG or dependency relation where possible; allow ordered sequences as a convenience, not as the only execution topology.

## 9. Workflows themselves can be optimized

### AutoFlow

AutoFlow generates and iteratively optimizes natural-language workflows.

Reference: [AutoFlow](https://arxiv.org/abs/2407.12821)

### AFlow

AFlow models workflows as graphs/code containing LLM-invoking nodes and searches over candidate workflow structures using execution feedback.

Reference: [AFlow: Automating Agentic Workflow Generation](https://arxiv.org/abs/2410.10762)

### Workflow optimization as a research category

A 2026 survey frames agentic systems as computation graphs and distinguishes reusable workflow structure from instantiated graphs and execution traces. It also distinguishes static workflow optimization from systems that adapt graph structure dynamically per task.

Reference: [Agentic Workflow Optimization survey](https://arxiv.org/html/2603.22386v1)

This suggests that a workflow language should remain declarative and inspectable. If workflow structure is represented as data, other systems can synthesize, compare, lint, optimize, or formally analyze workflows without rewriting the runtime.

**AgentFlow implication:** preserve a clear specification/runtime boundary and avoid hiding orchestration semantics inside opaque prompt text.

## 10. Workflow memory is procedural memory

### Agent Workflow Memory

Agent Workflow Memory stores reusable procedures learned from successful trajectories, rather than treating memory solely as factual or conversational recall.

Reference: [Agent Workflow Memory](https://arxiv.org/abs/2409.07429)

This suggests separating:

- **run state** — facts specific to the current execution; and
- **procedural knowledge** — reusable workflow fragments learned across runs.

**AgentFlow implication:** reusable phases, skills, policies, or workflow fragments may eventually deserve versioned references rather than being copied as prompt text into each workflow.

## 11. Durable state and resumability are first-class reliability features

Long-running agents expose system failures that are less visible in short conversations: process interruption, partial mutations, context loss, stale results, and expensive restarts.

Anthropic's engineering work on long-running agent systems emphasizes checkpointing and resuming useful state rather than replaying whole runs.

Reference: [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/harness-design-long-running-apps)

A robust workflow runtime benefits from explicit concepts such as:

- workflow base revision;
- active phase;
- phase-start revision;
- completed phase marker;
- checkpoint revision;
- completion marker;
- resumability policy; and
- stale-state detection.

**AgentFlow implication:** recovery semantics should be part of the workflow model rather than left entirely to individual tools or agents.

## 12. Agent runtimes increasingly resemble operating systems

### AIOS

AIOS treats agent infrastructure as an operating-system problem, separating scheduling, context, memory, storage, access control, models, and external tools from the individual agent implementation.

Reference: [AIOS: LLM Agent Operating System](https://arxiv.org/abs/2403.16971)

### AAFLOW

AAFLOW explores asynchronous, distributed workflow execution with explicit execution plans, batching, scheduling, and efficient data movement.

Reference: [AAFLOW](https://arxiv.org/abs/2605.02162)

This points toward a clean layering:

- the **specification** declares desired execution semantics;
- the **runtime** implements scheduling, caching, batching, model hosting, tool transport, isolation, and persistence.

**AgentFlow implication:** avoid encoding implementation-specific process mechanics into the portable workflow format unless they are genuinely semantic requirements.

## 13. Safety favors constrained effects over prompt-level promises

### ToolEmu

ToolEmu studies failures from tool-using agents and uses emulated environments to discover risky behavior before deployment.

Reference: [ToolEmu: Identifying the Risks of LM Agents with an LM-Emulated Sandbox](https://arxiv.org/abs/2309.15817)

### AgentDojo

AgentDojo provides a benchmark for indirect prompt injection against agents operating over untrusted tool results and realistic external applications.

Reference: [AgentDojo: A Dynamic Environment to Evaluate Prompt Injection Attacks and Defenses for LLM Agents](https://arxiv.org/abs/2406.13352)

The important systems conclusion is:

> Authorization should not exist only as natural-language instructions inside the model context.

Effectful capabilities should be enforced by the runtime through mechanisms such as:

- allowed tools;
- allowed workspace paths;
- protected resources;
- credential scoping;
- network policy;
- sandboxing;
- human approval gates;
- mutation postconditions; and
- immutable validation inputs.

**AgentFlow implication:** workspace boundaries, protected resources, tool permissions, and approval requirements should be machine-enforced schema concepts.

## 14. Formal and structural workflow verification is emerging

### Lean4Agent

Lean4Agent explores formal verification of agent workflows and trajectories using Lean 4 and reports a relationship between workflow verification conditions and downstream task performance.

Reference: [Lean4Agent](https://arxiv.org/abs/2606.06523)

### Design-time structural verification

A separate 2026 project models workflows as composable building blocks and checks structural compatibility properties before execution. The authors explicitly note that structural verification complements rather than replaces runtime security and domain-specific checks.

Reference: [Design-time verification of agent workflows](https://arxiv.org/html/2606.21565v1)

### Stateful tool-enabled systems

Recent work also formalizes verification questions for agents that interact with persistent operational data and stateful tools.

Reference: [Verification of stateful tool-enabled agent deployments](https://arxiv.org/abs/2608.03609)

**AgentFlow implication:** prefer fields with precise semantics over arbitrary embedded control prose. The more policy expressed structurally, the more the workflow can be linted, statically analyzed, compared, or formally verified before execution.

## 15. A synthesis for AgentFlow

The literature suggests dividing authority into three conceptual planes.

### Workflow plane

The workflow plane declares:

- actors and their capabilities;
- task graph and dependencies;
- state model;
- allowed mutations;
- protected resources;
- budgets;
- validation conditions;
- recovery policy;
- human checkpoints; and
- completion conditions.

### Execution plane

The execution plane permits flexible reasoning within those constraints. Techniques such as ReAct, search, reflection, specialist workers, workflow memory, or learned routing fit here.

### Assurance plane

The assurance plane owns:

- authorization;
- invariant checking;
- environment validation;
- independent auditing;
- checkpoints;
- recovery;
- human verification; and
- final completion assertions.

This separation captures a recurring finding across long-horizon reliability, multi-agent failure analysis, security research, and workflow verification: **the actor performing work should not be the only authority deciding that its work is valid.**

## 16. Design requirements suggested by the research

The following requirements appear especially well supported for a portable agent workflow specification.

| Research finding | Specification implication |
| --- | --- |
| Agents operate in multi-step reason/act/observe loops | Agent nodes represent bounded executions, not single text completions |
| Dynamic agents and deterministic workflows serve different purposes | Separate model autonomy from orchestration authority |
| Deliberation may require branching/search | Support conditional and bounded control flow |
| Independent tasks can run concurrently | Represent dependencies explicitly |
| Self-reflection is useful but not authoritative | Separate repair/reflection from deterministic validation |
| Multi-agent coordination fails in predictable ways | Define contracts, termination, and verification structurally |
| Multiple agents are valuable when capabilities genuinely differ | Model generic executors rather than persona-only agents |
| Long-running tasks are interruption-prone | Make state, checkpoints, and resume semantics first-class |
| Tool use creates real security boundaries | Enforce capabilities and mutation scope outside prompts |
| Human review is sometimes irreducible | Treat human verification as a durable workflow state |
| Workflows can themselves be optimized | Keep workflows declarative and machine-inspectable |
| Static verification is becoming practical | Prefer typed, semantically precise fields |
| Completion errors are a common orchestration failure | Make completion a separately validated transition |

## 17. Relevance to the current AgentFlow direction

The current AgentFlow design direction is strongly aligned with these findings when it emphasizes:

- explicit workflow state;
- bounded workspace mutation;
- protected resources;
- deterministic validation;
- bounded repair;
- durable phase checkpoints;
- resumable active phases;
- human verification; and
- final completion assertions separate from implementation work.

A particularly strong pattern is:

> **bounded mutation -> deterministic validation -> checkpoint -> advancement**

with a separately defined recovery path when validation fails.

That pattern combines lessons from tool-use safety, durable execution, long-horizon harness design, verification research, and multi-agent failure analysis while remaining compatible with different model providers and agent implementations.

## 18. Areas worth exploring in later revisions

The research suggests several possible post-`v1alpha1` directions:

1. **Explicit DAG dependencies and parallel execution** rather than relying primarily on ordered phase lists.
2. **Typed input/output artifacts** for stronger stage contracts.
3. **Generic executor types** covering models, tools, humans, remote agents, and composite workflows.
4. **Capability and credential scopes** independent of prompt instructions.
5. **Budgets** for model calls, tool calls, time, tokens, and repair attempts.
6. **Independent auditor nodes** with read-only permissions.
7. **Reusable workflow fragments or procedural memory references.**
8. **Trace schema** distinct from the reusable workflow definition.
9. **Static linting and structural verification rules.**
10. **Optimization metadata** so candidate workflows can be compared on quality, latency, and cost.
11. **Dynamic routing/search strategies** as optional execution policies rather than core control authority.
12. **Explicit evidence objects** connecting a completion claim to the checks that justify it.

These are research-informed directions, not requirements for the current specification.

## Research canon

A compact reading list for further design work:

- Yao et al. — [ReAct](https://arxiv.org/abs/2210.03629)
- Yao et al. — [Tree of Thoughts](https://arxiv.org/abs/2305.10601)
- Zhou et al. — [Language Agent Tree Search](https://arxiv.org/abs/2310.04406)
- Shinn et al. — [Reflexion](https://arxiv.org/abs/2303.11366)
- Hong et al. — [MetaGPT](https://arxiv.org/abs/2308.00352)
- Qian et al. — [ChatDev](https://arxiv.org/abs/2307.07924)
- Wu et al. — [AutoGen](https://arxiv.org/abs/2308.08155)
- Kim et al. — [LLMCompiler](https://arxiv.org/abs/2312.04511)
- Wang et al. — [Agent Workflow Memory](https://arxiv.org/abs/2409.07429)
- [AutoFlow](https://arxiv.org/abs/2407.12821)
- [AFlow](https://arxiv.org/abs/2410.10762)
- [Why Do Multi-Agent LLM Systems Fail?](https://arxiv.org/abs/2503.13657)
- [Multi-Agent Collaboration via Evolving Orchestration](https://arxiv.org/html/2505.19591)
- [Rethinking the Value of Multi-Agent Workflow](https://arxiv.org/html/2601.12307v1)
- [Agentic Workflow Optimization survey](https://arxiv.org/html/2603.22386v1)
- [AAFLOW](https://arxiv.org/abs/2605.02162)
- [Lean4Agent](https://arxiv.org/abs/2606.06523)
- [Design-time verification of agent workflows](https://arxiv.org/html/2606.21565v1)
- [LongHorizon-Harness](https://arxiv.org/abs/2608.01964)
- [Verification of stateful tool-enabled agent deployments](https://arxiv.org/abs/2608.03609)
- [AIOS](https://arxiv.org/abs/2403.16971)
- [ToolEmu](https://arxiv.org/abs/2309.15817)
- [AgentDojo](https://arxiv.org/abs/2406.13352)
- Anthropic — [Building effective agents](https://www.anthropic.com/engineering/building-effective-agents)
- Anthropic — [How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
- Anthropic — [Effective harnesses for long-running agents](https://www.anthropic.com/engineering/harness-design-long-running-apps)
- OpenAI — [Agent orchestration](https://developers.openai.com/api/docs/guides/agents/orchestration)
