# Make the activity envelope fingerprint-centric

## Context

[`2026-08-18-health-declaration.md`](2026-08-18-health-declaration.md) shipped
the activity probe envelope as `{status, fingerprint, observed_at}`, with
`status` a closed set of `none` / `idle` / `active`. Operating that shape
surfaced three problems.

`active` asserts nothing. Core's only use of it is "this instance declares,
collect its fingerprint" — exactly what `idle` also does, minus the narrowing.
The field turned out to be an annotation on how to read the fingerprint, not a
report of a state the probe observed, and its name promised the latter.

`idle` reads as a state adjacent to `stalled`. Both look like stillness from
outside, and the bit actually encodes *expected* stillness — a pardon, not a
state. Every explanation of the value had to walk that back.

`none` admits shapes with no single meaning. An envelope could carry both
`"status": "none"` and a fingerprint, in which case the fingerprint was
silently discarded; and nothing in the form of a `none` envelope makes "there
was no basis to judge" legible — it is a declared value that has to be looked
up rather than an absence that reads as one.

Separately, every failure of an activity probe — a non-zero exit, a render
failure, unparseable stdout — was indistinguishable from `none` at the report
level. A probe broken for days looked exactly like a surface with nothing to
say.

## Decision

The `status` enum is retired. The envelope is fingerprint-centric, with
exactly two legal shapes:

```json
{ "fingerprint": "abc", "observed_at": "..." }
{ "fingerprint": "abc", "silence_expected": true, "observed_at": "..." }
```

**`fingerprint` is required.** An envelope without one is invalid probe
output, treated like a parse error: reported, contributing nothing. It is not
"no basis".

**`silence_expected: true` is the pardon.** It says this fingerprint's
stability is intended, so the silence must not be counted. The narrow-only
semantics are unchanged — a probe may lower the expectation core derived from
`done_when`, never manufacture one. Because the field annotates a fingerprint
that must be present, an evidence-free pardon is no longer representable.

**No basis is structural absence: exit 0 with empty stdout.** A probe with
nothing to report emits no envelope, and the instance evaluates exactly as if
it declared no probe.

**Probe error is a non-zero exit.** For due-evaluation it is the same
non-contribution as no-basis, but it is surfaced in the health report as a
`probe_error` entry carrying the instance, the probe command, the exit code,
and a digest of stderr, and `plect status` prints it as a warning. A
persistently broken probe is observable as a fault rather than as silence.

The two channels are orthogonal: **stdout decides contribution, the exit code
decides the health of the probe itself.** Invalid output is the one crossing
case — the probe ran (exit 0) but printed something unusable — and it reports
as a fault too, because the alternatives are discarding it silently or
fabricating a fingerprint, and both let a broken probe pass for a quiet one.

The present-state specification is
[`../design/health-declaration.md`](../design/health-declaration.md).

## Consequences

The net effect on the specification is a removal: the enum, the
fingerprint-discard rule for `none`, and the prose interpreting ambiguous
shapes all disappear, replaced by one required field and one optional boolean.

Every shipped probe changes shape in the same commit: the tmux pane
fingerprint drops `status: "active"`, and the `claude` / `codex`
turn-boundary probes replace it with `silence_expected`, emitting nothing at
all where they previously emitted `{"status": "none"}`. User-owned probes
migrate by [`../migrations/activity-envelope-migration.md`](../migrations/activity-envelope-migration.md);
per the pre-1.0 compatibility policy there is no compatibility read of the
enum, and a surviving `status` envelope is rejected as invalid output rather
than silently reinterpreted.

The health report gains a `probe_errors` list. It is a report of the probes,
not of the session: entries never change the health state, because a probe
that cannot speak is not evidence that the surface it watches is stalled.

## Alternatives considered

### Keeping `status` and adding a fourth value for probe failure

Rejected. Probe failure is not something a probe can declare — a probe that
cannot run cannot print. Encoding it in the same field that the probe writes
puts the one outcome the probe cannot report into the channel only the probe
writes, which is why the exit code carries it instead.

### Keeping `none` as a declared value alongside empty stdout

Rejected. Two spellings of one outcome is exactly the ambiguity this decision
removes, and the declared spelling is the one that admits a discarded
fingerprint beside it.

### `silence_ok` / `quiet_expected` / `idle` as the field name

Rejected in favor of `silence_expected`. It names the thing being pardoned
(silence, the absence of fingerprint change) and the mode of the pardon (it is
expected, not merely tolerated), and it shares no vocabulary with the output
state `stalled` — which is precisely the confusion `idle` created.

### Making the pardon a fingerprint-less envelope

Rejected. It would restore the evidence-free pardon this shape exists to make
unrepresentable: a probe could excuse silence without attesting anything at
all, which is the single most dangerous thing a probe can do, because a wrong
pardon hides a real stall.

### Reporting probe errors as unhealthy

Rejected. The alive probe is what says the surface is gone. An activity probe
failing says the observation apparatus is broken, which is a fault worth
naming but not evidence about the surface — treating it as unhealthy would
escalate against sessions that are plainly fine whenever a probe's own
dependency (a missing `jq`, a stale state directory) breaks.
