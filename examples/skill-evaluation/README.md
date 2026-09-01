# Isolated AgentFlow skill evaluation

Never run the skill-iteration workflow against the AgentFlow source checkout.
AgentFlow actor workspaces contain a quarantined copy of the selected target
repository, so selecting the source repository would expose its implementation
to every actor.

To run the complete evaluation and automatically promote an accepted skill
revision back into this repository, use the trusted host launcher:

```sh
scripts/run-agentflow-skill-iteration.sh /tmp/agentflow-skill-evaluation
```

The launcher creates the dedicated fixture, then runs AgentFlow inside a
Bubblewrap filesystem that contains the fixture, system commands, a temporary
AgentFlow binary built from the current checkout, the complete Codex executable
bundle (including its tool host), and only the Codex authentication file. The
AgentFlow source checkout is not mounted, its path is not passed through the
environment, and actor invocations can inspect only their quarantine copies of
the public fixture. Network access remains available for the Codex provider.

After the workflow, deterministic gates, independent audit, and configured
human gate complete, the launcher checks that the source skill did not change
during evaluation and synchronizes only `skills/agentflow-spec/**` back into
this repository. It retains the fixture and reports for review. A failed,
interrupted, or stale-source run does not promote anything. Host-only baseline
state is retained beside the fixture at `<fixture>.host-control`; rerun the
same launcher command to resume an interrupted workflow. That directory is
never mounted into the Bubblewrap filesystem.

An exhausted validation-repair budget is durable acceptance state rather than
an interruption. After fixing an executor-level problem, explicitly abandon
that failed run and start a new invocation in the same fixture with:

```sh
scripts/run-agentflow-skill-iteration.sh --restart /tmp/agentflow-skill-evaluation
```

Restart uses AgentFlow's workflow-scoped reset inside the isolated filesystem;
it does not delete the fixture, reports, source checkout, or host baseline.

For fixture creation or workflow debugging without automatic promotion, use:

```sh
scripts/create-agentflow-skill-evaluation-workspace.sh /tmp/agentflow-skill-evaluation
```

The creator fails if the destination already exists. It copies only the public
skill package, the v1alpha4 authoring contract and schema, fixed evaluation
cases, deterministic harness, and workflow. It initializes
those files as a standalone Git repository; it does not copy `internal/`,
`provider/`, `cmd/`, `main.go`, `go.mod`, `go.sum`, or any Go source. The
repository-local workflow remains ignored and untracked so AgentFlow can keep
that runtime control file out of every actor snapshot.

Run an installed or previously built AgentFlow CLI from outside the source
checkout while explicitly selecting the fixture repository:

```sh
cd /tmp
agentflow run agentflow-skill-iteration -C /tmp/agentflow-skill-evaluation
```

Agent invocations receive only quarantine workspaces derived from that fixture.
The workflow also checks the isolation marker and rejects forbidden source
paths during every validation. After completion, review the isolated Git
history and compare its skill copy with the source checkout:

```sh
git -C /tmp/agentflow-skill-evaluation log --oneline --decorate
diff -ru skills/agentflow-spec /tmp/agentflow-skill-evaluation/skills/agentflow-spec
```

The fixture is retained until the operator removes it. Running `agentflow`
directly with `-C` selects the fixture but does not hide other host paths from
the provider process; use the trusted Bubblewrap launcher when source-code
non-disclosure is required.
