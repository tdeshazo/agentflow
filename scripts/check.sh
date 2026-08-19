#!/usr/bin/env bash
set -euo pipefail

# Canonical deterministic AgentFlow development gate.
# This gate never runs an agent or makes a live model call.

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

GATE_TMP="$(mktemp -d "${TMPDIR:-/tmp}/agentflow-check.XXXXXX")"
trap 'rm -rf "$GATE_TMP"' EXIT
export GOCACHE="$GATE_TMP/go-cache"

step() {
  printf '\n==> %s\n' "$1"
}

step "Go formatting"
mapfile -t GO_FILES < <(rg --files -g '*.go' -g '!vendor')
if ((${#GO_FILES[@]} > 0)); then
  UNFORMATTED="$(gofmt -l "${GO_FILES[@]}")"
  if [[ -n "$UNFORMATTED" ]]; then
    printf 'unformatted Go files:\n%s\n' "$UNFORMATTED" >&2
    exit 1
  fi
fi

step "Diff hygiene"
git diff --check
git diff --cached --check

step "Go tests"
go test ./...

step "Go vet"
go vet ./...

step "Race-enabled Go tests"
go test -race ./...

step "Deterministic self-hosting runtime"
go test ./internal/engine -run '^TestSelfHosting' -count=1

step "Self-hosting definition"
go run . validate -f examples/develop-agentflow.agent-workflow.yaml

step "Shipped AgentWorkflow definitions"
mapfile -t WORKFLOW_FILES < <(rg --files spec examples -g '*.yaml' -g '*.yml' | sort)
for workflow in "${WORKFLOW_FILES[@]}"; do
  printf '\n-- validating %s --\n' "$workflow"
  go run . validate -f "$workflow"
done

printf '\nAgentFlow quality gate passed.\n'
