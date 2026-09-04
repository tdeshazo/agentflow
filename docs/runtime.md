# Go interpreter reference

## Fresh invocation context and handoffs

Providers may negotiate `agentflow.dev/provider/v2` and
`agentflow.dev/invocation-context/v2`. The engine compiles fresh context from
the current objective, authority, workspace identity, direct accepted
dependencies, declared contract artifacts/evidence, accepted direct handoffs,
and selected validations. Compilation uses a fixed 65,536-byte canonical
ceiling; mandatory units fail before invocation and optional units are omitted
atomically with a receipt. The receipt contains only opaque unit IDs, digest,
and byte/count accounting, not omitted content.

Audit phases and phases with declared typed outputs require a provider that
advertises `provider/v2`, `invocation-context/v2`, and native
`agentflow.dev/handoff/v1` JSON output; incompatible providers fail preflight.
The engine validates strict
size, path, and secret-safety bounds, rejects blocked output, and publishes it
only after deterministic phase acceptance, bound to the run, node execution,
phase, and accepted commit. Later phases consume only accepted handoffs from
their direct dependencies on v2 compilation paths. Ordinary phases may retain
legacy v1 invocation behavior; local-command deliberately does not claim
structured output support.

The canonical runtime document is now
[docs/reference/runtime.md](reference/runtime.md). This compatibility entry
point remains for historical links.
