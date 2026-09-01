#!/bin/sh
set -eu

RESTART=false
if test "$#" -eq 1; then
  EVALUATION_ARGUMENT=$1
elif test "$#" -eq 2 && test "$1" = --restart; then
  RESTART=true
  EVALUATION_ARGUMENT=$2
else
  echo "usage: $0 [--restart] /path/to/evaluation-workspace" >&2
  exit 2
fi

SOURCE_ROOT=$(realpath "$(git rev-parse --show-toplevel)")
EVALUATION_ROOT=$(realpath -m "$EVALUATION_ARGUMENT")

case "$EVALUATION_ROOT" in
  /*) ;;
  *)
    echo "evaluation workspace must be an absolute path: $EVALUATION_ROOT" >&2
    exit 2
    ;;
esac

case "$EVALUATION_ROOT/" in
  "$SOURCE_ROOT/"*)
    echo "evaluation workspace must be outside the AgentFlow source checkout" >&2
    exit 1
    ;;
esac

for command in bwrap codex diff git go realpath rsync sh; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "required command is unavailable: $command" >&2
    exit 1
  }
done

CODEX_BIN=$(readlink -f "$(command -v codex)")
CODEX_BIN_DIR=$(dirname "$CODEX_BIN")
CODEX_CODE_MODE_HOST=$CODEX_BIN_DIR/codex-code-mode-host
CODEX_AUTH=${CODEX_HOME:-$HOME/.codex}/auth.json

test -x "$CODEX_CODE_MODE_HOST" || {
  echo "Codex code-mode host is unavailable: $CODEX_CODE_MODE_HOST" >&2
  exit 1
}
test -f "$CODEX_AUTH" || {
  echo "Codex authentication is unavailable: $CODEX_AUTH" >&2
  exit 1
}

BUILD_ROOT=$(mktemp -d /tmp/agentflow-skill-launcher.XXXXXX)
cleanup() {
  rm -rf "$BUILD_ROOT"
}
trap cleanup EXIT HUP INT TERM

CONTROL_ROOT=$EVALUATION_ROOT.host-control
SOURCE_BASELINE=$CONTROL_ROOT/source-skill
PROMOTED_BASELINE=$CONTROL_ROOT/promoted-skill

if test -e "$EVALUATION_ROOT"; then
  test -d "$EVALUATION_ROOT" || {
    echo "evaluation workspace is not a directory: $EVALUATION_ROOT" >&2
    exit 1
  }
  test -d "$SOURCE_BASELINE" || {
    echo "existing evaluation workspace has no trusted host baseline: $SOURCE_BASELINE" >&2
    echo "remove the old evaluation workspace or choose a new path" >&2
    exit 1
  }
  (
    cd "$EVALUATION_ROOT"
    sh scripts/check-skill-fixture.sh isolation
  )
else
  test ! -e "$CONTROL_ROOT" || {
    echo "host control path already exists without its evaluation workspace: $CONTROL_ROOT" >&2
    exit 1
  }
  mkdir -m 700 "$CONTROL_ROOT"
  cp -R "$SOURCE_ROOT/skills/agentflow-spec" "$SOURCE_BASELINE"
  if ! "$SOURCE_ROOT/scripts/create-agentflow-skill-evaluation-workspace.sh" "$EVALUATION_ROOT"; then
    rm -rf "$CONTROL_ROOT"
    exit 1
  fi
fi

EXPECTED_SOURCE=$SOURCE_BASELINE
if test -d "$PROMOTED_BASELINE"; then
  EXPECTED_SOURCE=$PROMOTED_BASELINE
fi
if ! diff -qr "$EXPECTED_SOURCE" "$SOURCE_ROOT/skills/agentflow-spec" >/dev/null; then
  echo "source skill changed outside this evaluation; refusing to run or overwrite it" >&2
  exit 1
fi

AGENTFLOW_BIN=$BUILD_ROOT/agentflow
go build -o "$AGENTFLOW_BIN" .

run_isolated_agentflow() {
  bwrap \
    --die-with-parent \
    --new-session \
    --unshare-all \
    --share-net \
    --clearenv \
    --ro-bind /usr /usr \
    --ro-bind-try /lib /lib \
    --ro-bind-try /lib64 /lib64 \
    --ro-bind /etc /etc \
    --symlink usr/bin /bin \
    --proc /proc \
    --dev /dev \
    --tmpfs /tmp \
    --dir /opt \
    --dir /opt/codex \
    --dir /opt/agentflow \
    --dir /opt/agentflow/bin \
    --dir /home \
    --dir /home/agentflow \
    --dir /home/agentflow/.codex \
    --ro-bind "$CODEX_AUTH" /home/agentflow/.codex/auth.json \
    --ro-bind "$CODEX_BIN_DIR" /opt/codex/bin \
    --ro-bind "$AGENTFLOW_BIN" /opt/agentflow/bin/agentflow \
    --bind "$EVALUATION_ROOT" /workspace \
    --setenv HOME /home/agentflow \
    --setenv CODEX_HOME /home/agentflow/.codex \
    --setenv PATH /opt/agentflow/bin:/opt/codex/bin:/usr/bin:/bin \
    --setenv NO_COLOR 1 \
    --chdir /workspace \
    /opt/agentflow/bin/agentflow "$@" -C /workspace
}

run_skill_iteration() {
  set -- run agentflow-skill-iteration
  if test -n "${AUTHOR_MODEL:-}"; then
    set -- "$@" --set "author_model=$AUTHOR_MODEL"
  fi
  if test -n "${EVALUATOR_MODEL:-}"; then
    set -- "$@" --set "evaluator_model=$EVALUATOR_MODEL"
  fi
  run_isolated_agentflow "$@"
}

if test "$RESTART" = true; then
  run_isolated_agentflow reset agentflow-skill-iteration
fi
run_skill_iteration

(
  cd "$EVALUATION_ROOT"
  sh scripts/check-skill-fixture.sh all
)

if ! diff -qr "$EXPECTED_SOURCE" "$SOURCE_ROOT/skills/agentflow-spec" >/dev/null; then
  echo "source skill changed during evaluation; refusing to overwrite it" >&2
  exit 1
fi

rsync -a --delete \
  "$EVALUATION_ROOT/skills/agentflow-spec/" \
  "$SOURCE_ROOT/skills/agentflow-spec/"

git -C "$SOURCE_ROOT" diff --check -- skills/agentflow-spec
mkdir -p "$PROMOTED_BASELINE"
rsync -a --delete \
  "$EVALUATION_ROOT/skills/agentflow-spec/" \
  "$PROMOTED_BASELINE/"

echo "promoted accepted AgentFlow skill revision into: $SOURCE_ROOT/skills/agentflow-spec"
echo "retained isolated evaluation workspace for review: $EVALUATION_ROOT"
echo "retained host-only recovery state: $CONTROL_ROOT"
