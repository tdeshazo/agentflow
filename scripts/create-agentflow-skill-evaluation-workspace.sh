#!/bin/sh
set -eu

if test "$#" -ne 1; then
  echo "usage: $0 /path/to/new/evaluation-workspace" >&2
  exit 2
fi

SOURCE_ROOT=$(git rev-parse --show-toplevel)
EVALUATION_ROOT=$1

if test -e "$EVALUATION_ROOT"; then
  echo "evaluation workspace already exists: $EVALUATION_ROOT" >&2
  exit 1
fi

mkdir -p \
  "$EVALUATION_ROOT/.agentflow/workflows" \
  "$EVALUATION_ROOT/cases" \
  "$EVALUATION_ROOT/docs/reference" \
  "$EVALUATION_ROOT/schema" \
  "$EVALUATION_ROOT/scripts" \
  "$EVALUATION_ROOT/skills"

cp -R "$SOURCE_ROOT/skills/agentflow-spec" "$EVALUATION_ROOT/skills/"
cp "$SOURCE_ROOT/docs/reference/agentflow-v1alpha4.md" "$EVALUATION_ROOT/docs/reference/"
cp "$SOURCE_ROOT/schema/v1alpha4.schema.json" "$EVALUATION_ROOT/schema/"
cp "$SOURCE_ROOT/examples/skill-evaluation/ISOLATED_SKILL_EVALUATION_WORKSPACE" "$EVALUATION_ROOT/"
cp "$SOURCE_ROOT/examples/skill-evaluation/README.md" "$EVALUATION_ROOT/README.md"
cp "$SOURCE_ROOT/examples/skill-evaluation/fixture.gitignore" "$EVALUATION_ROOT/.gitignore"
cp "$SOURCE_ROOT/examples/skill-evaluation/cases/baseline.md" "$EVALUATION_ROOT/cases/"
cp "$SOURCE_ROOT/examples/skill-evaluation/cases/forward-test.md" "$EVALUATION_ROOT/cases/"
cp "$SOURCE_ROOT/examples/skill-evaluation/scripts/check-skill-fixture.sh" "$EVALUATION_ROOT/scripts/"
cp "$SOURCE_ROOT/examples/representative/agentflow-skill-iteration.agent-workflow.yaml" \
  "$EVALUATION_ROOT/.agentflow/workflows/agentflow-skill-iteration.yaml"

if find "$EVALUATION_ROOT" -type l -print | rg -q .; then
  echo "refusing to create an evaluation fixture containing symbolic links" >&2
  exit 1
fi

chmod +x "$EVALUATION_ROOT/scripts/check-skill-fixture.sh"
git -C "$EVALUATION_ROOT" init -q
git -C "$EVALUATION_ROOT" config user.name 'AgentFlow Skill Evaluation'
git -C "$EVALUATION_ROOT" config user.email 'agentflow-skill-evaluation@example.invalid'
git -C "$EVALUATION_ROOT" add .
git -C "$EVALUATION_ROOT" commit -qm 'initialize isolated AgentFlow skill evaluation'

echo "created isolated AgentFlow skill evaluation workspace: $EVALUATION_ROOT"
echo "run from outside the source checkout: agentflow run agentflow-skill-iteration -C $EVALUATION_ROOT"
echo "or use scripts/run-agentflow-skill-iteration.sh from the source checkout for isolated execution and automatic promotion"
