# AgentFlow self-hosting MVP evidence

Status: Passed

Dogfood workflow: `examples/develop-agentflow.agent-workflow.yaml`

Self-hosted implementation commit: `2e8712e11acbc8a4ec1aa00ca96f68b9013284e7`

Final durable state: `completed`

Restart proof: accepted `implement` and `audit` phases were skipped on restart.

Canonical gate: passed

## Scope and starting point

ROADMAP.md identifies Priority 3, Self-hosting development workflow, as the
AgentFlow MVP gate. The pre-dogfood audit/base was commit
`d4dc5004e1ee5fd34734537f1c91bf732cd5e7e0`, whose subject is
`Harden AgentFlow MVP: final-pre-dogfood-audit`. The durable run status records
that commit as the workflow base on branch `main`.

## What the run demonstrated

- The real self-hosted product change was machine-readable output for
  `agentflow status --json`. Commit `2e8712e11acbc8a4ec1aa00ca96f68b9013284e7`
  contains that implementation and its deterministic tests.
- The change was performed through the AgentFlow `implement` phase. The
  retained `implement.done` marker records the implementation commit.
- A distinct Terra `audit` phase existed and was accepted. The retained
  `audit.done` marker records the same accepted implementation commit.
- After accepted work, the interpreter was restarted. The retained
  `selfhost-resume.log` records that both accepted phases were skipped, while
  the repository-owned deterministic checks ran again.
- The final durable completion commit recorded by `final-status.json` is
  `f92bd7922efdbc3ec04f0b52e2abf872a172e9cf`. The `complete` marker and the
  `agentflow status --json` result agree that the durable state is completed.
- Repository-owned deterministic validation remained authoritative. The
  workflow routes its implementation, audit, and final validation through
  `scripts/check.sh`; retained proof records the canonical AgentFlow quality
  gate passing.

## Retained proof sources

The record above is derived from the current Git history, the workflow
definition, `agentflow status --json`, and these retained local artifacts:

- `.git/agentflow-mvp-recovery/selfhost-resume.log`
- `.git/agentflow-mvp-recovery/final-status.json`
- `.git/agentflow-mvp-recovery/selfhost-human-gate-head`
- `.git/agentflow-mvp-recovery/selfhost-complete`

No prompts, model reasoning, environment values, secrets, or complete agent
logs are reproduced here.

Review agent: Passed (Terra/high)
