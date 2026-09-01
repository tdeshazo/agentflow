#!/bin/sh
set -eu

mode=${1:-}

fail() {
  echo "skill fixture check failed: $1" >&2
  exit 1
}

check_isolation() {
  marker='AgentFlow source code is intentionally absent from this workspace.'
  workflow_path='.agentflow/workflows/agentflow-skill-iteration.yaml'
  rg -q -F "$marker" ISOLATED_SKILL_EVALUATION_WORKSPACE || fail 'missing isolation marker'
  test -f "$workflow_path" || fail 'missing private workflow control file'
  git check-ignore -q "$workflow_path" || fail 'private workflow control file is not ignored'
  if git ls-files --error-unmatch "$workflow_path" >/dev/null 2>&1; then
    fail 'private workflow control file is tracked'
  fi
  for path in internal provider cmd go.mod go.sum main.go; do
    test ! -e "$path" || fail "forbidden AgentFlow source path present: $path"
  done
  if find . -path ./.git -prune -o -type f -name '*.go' -print | rg -q .; then
    fail 'Go source is present'
  fi
  if find . -path ./.git -prune -o -type l -print | rg -q .; then
    fail 'symbolic links are not allowed in the isolated fixture'
  fi
}

check_contract() {
  test "$(sed -n '1p' skills/agentflow-spec/SKILL.md)" = '---' || fail 'invalid skill frontmatter opening'
  rg -q '^name: agentflow-spec$' skills/agentflow-spec/SKILL.md || fail 'missing skill name'
  rg -q '^description: .+' skills/agentflow-spec/SKILL.md || fail 'missing skill description'
  rg -q 'agentflow.dev/v1alpha4' skills/agentflow-spec/SKILL.md || fail 'skill omits v1alpha4'
  if rg -q 'agentflow.dev/v1alpha(1|2|3)' skills/agentflow-spec; then
    fail 'skill contains pre-v1alpha4 API syntax'
  fi
  rg -q '../../docs/reference/agentflow-v1alpha4.md' skills/agentflow-spec/SKILL.md || fail 'missing v1alpha4 contract link'
  rg -q '../../schema/v1alpha4.schema.json' skills/agentflow-spec/SKILL.md || fail 'missing v1alpha4 schema link'
  rg -q 'Produce v1alpha4 syntax only' skills/agentflow-spec/SKILL.md || fail 'missing v1alpha4-only scope'
  rg -q 'expanded plan' skills/agentflow-spec/SKILL.md || fail 'missing expanded-plan review rule'
  git diff --check
}

check_baseline() {
  file=evaluation/baseline.md
  test -s "$file" || fail 'missing baseline report'
  for heading in '# Baseline forward test' '## Scenario: author' '## Scenario: review' '## Scenario: recover' '## Findings' '## Recommended skill changes'; do
    rg -q -F -x "$heading" "$file" || fail "baseline report omits $heading"
  done
}

check_pass_verdict() {
  file=$1
  report=$2
  verdict_count=$(rg -c '^Verdict:' "$file" || true)
  test "$verdict_count" = 1 || fail "$report report must contain exactly one verdict"
  final_line=$(sed '/^[[:space:]]*$/d' "$file" | sed -n '$p')
  test "$final_line" = 'Verdict: PASS' || fail "$report verdict must be the final nonblank line and exactly Verdict: PASS"
}

check_forward_test() {
  file=evaluation/forward-test.md
  test -s "$file" || fail 'missing forward-test report'
  for heading in '# Independent forward test' '## Scenario: author' '## Scenario: review' '## Scenario: work-items' '## Findings'; do
    rg -q -F -x "$heading" "$file" || fail "forward-test report omits $heading"
  done
  check_pass_verdict "$file" 'forward-test'
}

check_audit() {
  file=evaluation/audit.md
  test -s "$file" || fail 'missing audit report'
  for heading in '# AgentFlow skill iteration audit' '## Authority and scope' '## Forward-test assessment' '## Residual risks'; do
    rg -q -F -x "$heading" "$file" || fail "audit report omits $heading"
  done
  check_pass_verdict "$file" 'audit'
}

case "$mode" in
  isolation)
    check_isolation
    ;;
  contract)
    check_isolation
    check_contract
    ;;
  baseline)
    check_baseline
    ;;
  forward-test)
    check_forward_test
    ;;
  audit)
    check_audit
    ;;
  all)
    check_isolation
    check_contract
    check_baseline
    check_forward_test
    check_audit
    ;;
  *)
    fail 'usage: check-skill-fixture.sh {isolation|contract|baseline|forward-test|audit|all}'
    ;;
esac
