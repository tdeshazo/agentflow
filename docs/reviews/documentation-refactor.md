# Documentation refactor review

Date: 2026-08-17

## Scope examined

Compared the working tree with workflow base `c8e3ef8`; inspected the target
documentation tree, `ROADMAP.md`, retained research and self-hosting MVP
evidence, the v1alpha1 field guide, runtime reference, CLI/runtime source,
self-hosting workflow, and `scripts/check.sh`.

Reviewed documentation surfaces: `docs/README.md`, `docs/architecture/`,
`docs/guides/`, `docs/planning/`, `docs/reference/`, `docs/research/`, and
`docs/evidence/`.

## Findings and corrections

- The documentation now has one front door and distinct architecture, guides,
  planning, reference, research, evidence, and review categories. Evidence is
  explicitly non-normative; research remains exploratory.
- `ROADMAP.md`, research, and MVP evidence are byte-for-byte unchanged from the
  workflow base. The field guide and runtime reference retain their relocated
  technical content; the three former top-level documentation paths are brief
  compatibility pointers.
- The authority and development/self-hosting guides match the implemented
  runtime: agents act within policy, deterministic validation accepts phases,
  accepted state is Git-backed and durable, and provider output is not workflow
  authority. `scripts/check.sh` is the canonical deterministic gate.
- Corrected one navigation defect: added the missing Guides category index and
  linked the documentation front door to it.
- The base-to-HEAD diff contains only `README.md` and `docs/**`; no source,
  workflow, specification, skill, governance, research-source, MVP-evidence,
  or quality-policy file changed.

## Deterministic verification

`./scripts/check.sh`

Status: Passed
