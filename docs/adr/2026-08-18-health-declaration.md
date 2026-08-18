# Unify health declaration into a [health] table

## Context

The health output vocabulary is unified — `healthy` / `unhealthy` / `stalled` /
`undeclared`, the `plect ls` HEALTH column. The input side was not. Three
surfaces from three naming generations described the same two facts:

- **task `healthcheck` (scalar)** — the liveness probe, declared by every
  shipped runtime task.
- **task `movement_signal`** — created during the health-deepening arc and
  never declared anywhere: a twin surface born unused.
- **workflow `[tick.movement_source]`** — the form actually wired in practice,
  pointing at a user-owned pane-fingerprint script. The lockstep that wired
  `movement_source` and renamed that script let `movement_signal` pass through
  untouched, which is how two surfaces for one concept survived.

The name for that one concept drifted too: `progress_signal` became
`movement` (progress was rightly rejected — a fingerprint attests observable
change, not goal progress), while the agent turn-boundary hooks had been
calling the same concept family **activity** all along
(`claude-agent-activity`, `codex-agent-activity`).

The consequence was that default health coverage depended on user-owned
configuration and a user-owned script. A session got stall detection only if
its workflow happened to wire a fingerprint source.

This decision lands before the task-nesting implementation. Nesting's
inner-owned field list and its layer-health direction both touch this surface,
so implementing nesting against the two-scalar shape would guarantee rework.

## Decision

One task-level table declares how a task's health is determined:

```toml
[health]
alive    = '...'   # liveness probe: exit-code semantics
activity = '...'   # activity probe: JSON fingerprint envelope
```

The activity envelope is `{status, fingerprint, observed_at}`. The
fingerprint carries the fact of activity; `status` carries what its absence
would mean, as a closed set of three: `none` (silence means nothing — nothing
was observed, so the observation is discarded), `idle` (silence is normal,
pardoned by the surface's own phase — the expectation is narrowed), and
`active` (silence stands unpardoned). `active` is the residual: something was
observed and `idle` was not established, so activity cannot be ruled out.

Only `idle` requires positive establishment, and only `idle` changes what
core concludes. A wrong pardon hides a real stall; a wrongly withheld pardon
is safe, because core's own `done_when`-derived expectation still has to
agree before anything is called stalled. The accusation is always core's; a
probe holds only the pardon channel.

The field is spelled `status` on the exit-status precedent: it names the
outcome classification of an attempt, and the values read naturally under it.

`setup` / `cleanup` / `[health]` is the universal task lifecycle trio. The
present-state specification — worked examples, validation rules, envelope
fields, and composition — is
[`../design/health-declaration.md`](../design/health-declaration.md).

**The table is named `[health]`, not `[healthcheck]`.** Config tables name the
declared aspect rather than the act, following `[terminal]` and `[done_when]`.
It closes the loop with the ratified output vocabulary: the `[health]` table
declares how the HEALTH column is determined. A fresh name also keeps the
migration grep-clean — `healthcheck =` becomes `[health].alive` — instead of
reusing the retired scalar's spelling, where a half-migrated file would still
match every search for the old name.

**The probe vocabulary is `activity`, not `movement`.** It is honest about what
a fingerprint attests, and it converges with the existing agent-activity hook
naming instead of adding a fourth generation to the lineage.

**Three surfaces are retired**: the `healthcheck` scalar, task
`movement_signal`, and workflow `[tick.movement_source]`. Each is a named load
error rather than a silently dropped key. Kubernetes probes — liveness and
readiness as distinct probes inside one framework — are the precedent shape. A
readiness-style third probe is an explicit non-goal: health answers whether a
surface is present and moving, not whether traffic may be sent to it.

**Probes are plugin-shipped capability.** The tmux task declares a
capture-based generic activity fingerprint. The claude, codex, and
`codex_exec` runtime tasks declare their turn-boundary activity hooks as their
activity probe: the hook records each boundary and a `probe` verb on the same
plugin executable reads it back, so hook and fingerprint are two
implementations of one probe. This dissolves the user-owned fingerprint script
and removes all user-config wiring for default health coverage.

**Layer-scoped health under nesting.** An outer task may declare `[health]`
for its own layer's resources — the layer-symmetry completion of
`setup`/`cleanup`, where each layer owns its own lifecycle trio. Composition is
dual: `alive` is the AND across layers, with the failing layer named in the
report; `activity` is the OR across layers. A layer declaring no `[health]`
contributes nothing to either composition. Layers never override each other's
probes — each declares only its own — so no conflict-resolution rule is
needed.

## Consequences

Every task definition declaring the old scalars fails to load until migrated,
including user-owned overlays. The one-time procedure is
[`../migrations/health-table-migration.md`](../migrations/health-table-migration.md);
per the pre-1.0 compatibility policy there is no compatibility read of the old
keys.

The persisted `HealthState.last_movement_at` becomes `last_activity_at`, and
the health report's `movement_*` fields become `activity_*` across the CLI,
the MCP surface, and health escalation metadata. A subscriber matching on
those names updates with the migration.

An activity probe's envelope is rejected at parse time when `status` is absent
or unrecognized. A probe author gets a named error instead of a silent
fallback, because neither candidate default is safe: `idle` would pardon every
silence and hide real stalls, and `active` would accuse probes that never
opted in.

The health report's own `activity_expected` field is renamed `activity_due`.
It is core's derived accusation, not a probe's report, and the old spelling
would have kept the retired envelope field's name alive on a second surface.

A workflow's `[healthcheck]` table is untouched: it declares the sampling
cycle (period, stall threshold, re-notification), not what health means.
Because that spelling survives, the migration's absence sweep is scoped to the
retired task-level scalar rather than to the bare word.

Implementation obligations carried by this decision:

- The task-nesting design and ADR replace `healthcheck` + `movement_signal` in
  their inner-owned field lists with layer-declarable `[health]`, and state the
  AND/OR composition above. That work lands in the nesting documents, which are
  in flight; this ADR is the source they follow.
- Any user-owned workflow declaring `[tick.movement_source]` drops it, and the
  user-owned pane-fingerprint script it pointed at is retired — the tmux
  plugin's probe replaces it.

## Alternatives considered

### `[healthcheck]` as the table name

Rejected. It names the act rather than the declared aspect, breaking with
`[terminal]` and `[done_when]`, and it collides with the workflow-level
`[healthcheck]` cycle table that is not being retired. Reusing the retired
scalar's spelling would also leave the migration unable to distinguish a
migrated file from an un-migrated one by search.

### `movement` as the probe vocabulary

Rejected. `movement` was itself the correction of `progress_signal` — a
fingerprint attests observable change, not progress toward a goal — and it is
accurate on that point. But the turn-boundary hooks had been named
`*-agent-activity` since before either spelling existed, so keeping `movement`
would have preserved a split between what the probe is called in config and
what the thing implementing it is called on disk. `activity` is equally honest
about attesting change and collapses the lineage instead of extending it.

### A four-value status set with a separate `opaque`

Rejected as an intermediate. The set was `none` / `opaque` / `idle` /
`active`, where `opaque` meant "observed, not interpretable further" and
`active` meant "observed, activity is due". Because no status may manufacture
an expectation, the two drove exactly the same core decision — one machine
behavior wearing two names, a distinction that lives only in prose and has to
be explained away wherever the set is documented.

Redefining `active` as the residual — observed, and `idle` not established —
absorbs the honesty concern that motivated `opaque`. A pane fingerprint that
cannot establish quiet is normal is not making a claim it lacks grounds for
when it reports `active`; it is declining to pardon, which is exactly what it
knows. That leaves one value per core behavior: `none` discards, `idle`
narrows, `active` counts without narrowing.

### Two booleans (`supported` + `activity_expected`) instead of a status enum

Rejected. The pair fails on both counts a sum type is for. It admits a
combination that means nothing — `supported: false` together with
`activity_expected: true` asserts an expectation about an instance that just
declared it has no basis to judge one — so the parser accepts states the
domain does not have. And the meaning of a probe that declines to pardon
silence exists only as a default on an omitted field, where either default is
wrong for someone: defaulting `activity_expected` to false makes every
generic probe silently narrow the expectation core derived from `done_when`,
suppressing the stall detection the probe exists to feed, and defaulting it
to true makes any probe that legitimately means "quiet is normal" say so
explicitly or produce false stalls. Under `status` that reading is a value
like any other, and illegal combinations stop being representable.

The debate this settles is whether `activity_expected` should exist at all.
It had no producer in the shipped tree — both agent tasks declined to emit
it — which by this ADR's own test for a readiness probe made it an extension
point with no present consumer. Folding it into `status` resolves that
without losing the narrowing capability: every value in the set is emitted by
a shipped probe.

### A readiness probe alongside alive and activity

Rejected as a non-goal. Kubernetes needs readiness because traffic routing is
a distinct decision from restarting a container. Plecture has no analogous
consumer: nothing routes to a session on the strength of a readiness verdict.
A third probe would ship an extension point with no present consumer.

### AND composition for activity

Rejected. Requiring every declared activity probe to report fresh evidence
would make a quiet layer beside an active agent read as a stall — a sidecar
that legitimately sits idle while the agent works would flip the session to
`stalled` and escalate against a session that is plainly moving. Activity is
evidence of life, so any evidence suffices; the masking risk that OR
introduces is handled by declaring probes only on resources whose activity
indicates session progress.

### Keeping the session-scoped `[tick.movement_source]` alongside `[health]`

Rejected. Its only real use was pointing at a user-owned pane-fingerprint
script, which the tmux plugin's own probe now supplies. Keeping it would leave
two places to declare one fact, differing only in scope, and would preserve
the user-config wiring this decision exists to remove.
