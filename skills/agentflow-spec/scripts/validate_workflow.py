#!/usr/bin/env python3
"""Validate AgentFlow workflow YAML without running any agents.

Use the installed ``agentflow`` CLI when it is available. When this script is
run from an AgentFlow source checkout, it falls back to ``go run .`` so skill
authors can validate a workflow during local development without installing a
binary first.
"""

from __future__ import annotations

import argparse
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Sequence


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "workflows",
        metavar="WORKFLOW",
        nargs="+",
        type=Path,
        help="AgentWorkflow YAML file to validate",
    )
    parser.add_argument(
        "--agentflow-bin",
        metavar="PATH",
        help="AgentFlow executable to use instead of resolving one from PATH",
    )
    parser.add_argument(
        "--repo",
        metavar="PATH",
        type=Path,
        help="repository passed to the AgentFlow CLI with -C",
    )
    parser.add_argument(
        "--expanded-plan",
        action="store_true",
        help="also print the normalized expanded plan after each successful validation",
    )
    return parser.parse_args(argv)


def source_checkout() -> Path | None:
    """Return this repository's root when the bundled Go CLI is available."""
    root = Path(__file__).resolve().parents[3]
    return root if (root / "go.mod").is_file() else None


def command_prefix(agentflow_bin: str | None, checkout: Path | None) -> tuple[list[str], Path | None]:
    if agentflow_bin:
        return [agentflow_bin], None
    if binary := shutil.which("agentflow"):
        return [binary], None
    if checkout:
        return ["go", "run", "."], checkout
    raise FileNotFoundError(
        "could not find the agentflow CLI; install it, add it to PATH, or pass --agentflow-bin"
    )


def run(command: list[str], *, cwd: Path | None) -> int:
    try:
        return subprocess.run(command, cwd=cwd, check=False).returncode
    except OSError as error:
        print(f"validate_workflow.py: {error}", file=sys.stderr)
        return 1


def main(argv: Sequence[str]) -> int:
    args = parse_args(argv)
    try:
        prefix, cwd = command_prefix(args.agentflow_bin, source_checkout())
    except FileNotFoundError as error:
        print(f"validate_workflow.py: {error}", file=sys.stderr)
        return 2

    repo_args = ["-C", str(args.repo.resolve())] if args.repo else []
    for workflow in args.workflows:
        workflow = workflow.resolve()
        if not workflow.is_file():
            print(f"validate_workflow.py: workflow is not a file: {workflow}", file=sys.stderr)
            return 2

        if run([*prefix, "validate", "-f", str(workflow), *repo_args], cwd=cwd) != 0:
            return 1
        if args.expanded_plan and run(
            [*prefix, "plan", "--expanded", "-f", str(workflow), *repo_args], cwd=cwd
        ) != 0:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
