#!/usr/bin/env bash
set -euo pipefail

# Canonical deterministic AgentFlow development gate.
# This gate never runs an agent or makes a live model call.

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

GATE_TMP="$(mktemp -d "${TMPDIR:-/tmp}/agentflow-check.XXXXXX")"
trap 'rm -rf "$GATE_TMP"' EXIT

# Preserve Go's normal persistent build cache so repeated local and CI runs can
# reuse compilation artifacts. Test-result caching is disabled explicitly below
# so every quality-gate invocation still executes the tests.

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
go test -count=1 ./...

step "Go vet"
go vet ./...

step "Race-enabled Go tests"
go test -race -count=1 ./...

step "Build AgentFlow CLI"
AGENTFLOW="$GATE_TMP/agentflow"
go build -o "$AGENTFLOW" .

step "Reference AgentWorkflow definition"
mapfile -t WORKFLOW_FILES < <(rg --files spec -g '*.yaml' -g '*.yml' | sort)
for workflow in "${WORKFLOW_FILES[@]}"; do
  printf '\n-- validating %s --\n' "$workflow"
  "$AGENTFLOW" validate -f "$workflow"
done

printf '\nAgentFlow quality gate passed.\n'
