# Health declaration

This design is governed by
[`../adr/2026-08-18-health-declaration.md`](../adr/2026-08-18-health-declaration.md).

## Design Core

A task declares how its health is determined in one `[health]` table holding
two probes:

- **`alive`** — the liveness probe. Exit-code semantics: zero means the
  execution surface this task owns is present.
- **`activity`** — the activity probe. Fingerprint semantics: it writes a JSON
  activity envelope whose opaque fingerprint core compares across evaluations.

`setup`, `cleanup`, and `[health]` are the universal task lifecycle trio: what
brings the surface up, what takes it down, and what says whether it is still
there and moving.

Probes are plugin-shipped capability. A plugin that owns a surface ships the
probes for it, so default health coverage requires no user configuration.

## Worked example

```toml
# The tmux plugin's pane task: liveness is a session lookup, and activity is
# a fingerprint of the pane's visible contents.
[health]
alive = 'tmux has-session -t {{.Self.session_name}}'
activity = '''
PANE=$(tmux capture-pane -p -t {{.Self.session_name}}) || exit 1
FP=$(printf '%s' "$PANE" | cksum | tr -d ' \t')
jq -nc --arg fp "$FP" --arg at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{status: "active", fingerprint: $fp, observed_at: $at}'
'''
```

```toml
# An agent-runtime task: liveness is a process check, and activity is the
# turn-boundary record the same plugin's hook writes.
[health]
alive    = 'kill -0 {{.Self.pid}}'
activity = '{{bin "codex-agent-activity"}} probe {{.SessionName}}'
```

A hook and a pane fingerprint are two implementations of one probe. Both reach
core through `activity`, so core has one probe shape to evaluate rather than
one per implementation technique.

## Validation rules

- `[health]` is optional. A task declaring no table contributes nothing to
  health.
- `alive` and `activity` are each optional and independent: neither implies
  the other. A `[health]` table declaring neither is a load error — the only
  reason to write the header is to declare a probe.
- Unknown keys under `[health]` are a load error naming the offending key. A
  readiness-style third probe is a non-goal: health answers "is this surface
  present and moving", not "may traffic be sent".
- Each member is a Go-template-rendered shell command string. Single-line and
  multi-line TOML strings are valid; arrays are invalid.
- Both members render against the same context as `setup`/`cleanup`: the
  task's own outputs (`.Self`), its resolved node inputs (`.Inputs`), and
  session vars (`.SessionName`, `.WorkspaceDirPath`, ...). `{{bin "..."}}`
  resolves against the declaring plugin.
- A task definition is replaced wholesale by a deeper cascade layer, so a
  user-owned overlay of a plugin task carries its own complete `[health]`.

## Probe semantics

### alive

Run via `bash -c`. Exit zero means the surface is present. A non-zero exit or
a render failure makes the session unhealthy, and the failing task instance is
named in the reported reason. The first failing probe ends the evaluation.

### activity

Run via `bash -c`. Stdout is the activity envelope:

```json
{
  "status": "idle",
  "fingerprint": "waiting:7",
  "observed_at": "2026-08-18T00:04:06Z"
}
```

| Field | Meaning |
|---|---|
| `status` | What the absence of new activity would mean, as the probe reads its own surface. One of the three values below. Required — an absent or unrecognized value is a parse error. |
| `fingerprint` | An opaque token that changes whenever the probe observes new activity. Core never parses it, only compares it. |
| `observed_at` | RFC3339. Absent means the probe reports no timestamp of its own. |

The fingerprint carries the fact of activity; `status` carries what its
absence would mean:

| Value | Silence means | Core's response |
|---|---|---|
| `none` | Nothing — the attempt found nothing to observe. | Discard the observation, the same as declaring no probe at all. |
| `idle` | Silence is normal, pardoned by the surface's own phase. | Count the fingerprint. Narrow the declaring instance's expectation. |
| `active` | Silence stands unpardoned. | Count the fingerprint. Silence becomes stall evidence where core's own expectation agrees. |

`active` is the residual: something was observed and `idle` was not
established, so activity cannot be ruled out. A generic surface fingerprint
reports it honestly — a pane's contents cannot establish that quiet is
normal.

Only `idle` requires the probe to have positively established anything (the
turn is over, the queue is empty), and only `idle` changes what core
concludes. The asymmetry is deliberate: a wrong pardon hides a real stall,
while a wrongly withheld pardon is safe, because core's own
`done_when`-derived expectation still has to agree before anything is called
stalled. The accusation is always core's; a probe holds only the pardon
channel.

A non-zero exit, a render failure, or unparseable stdout is an error, and the
instance contributes no activity evidence for that evaluation — the same
outcome as `"status": "none"`, and distinct from a fabricated fingerprint.

Freshness is judged against core's own clock, not against `observed_at`: core
records when it first saw a given fingerprint, and a fingerprint that has not
changed within the workflow's stall threshold reads as stalled once activity
is expected.

## Composition across instances

A session's health is composed from every produced run-scoped task instance:

- **`alive` composes by AND.** Liveness is a chain of necessary resources, so
  any failing probe makes the session unhealthy, reported with the failing
  instance named.
- **`activity` composes by OR.** Activity is evidence of life, so evidence
  from any instance counts. Core joins the declaring instances' fingerprints
  into one composite and treats a change anywhere in it as the session having
  moved.

An instance declaring no `[health]` contributes nothing to either composition:
it is vacuous in the AND and casts no vote in the OR. So does one whose
activity probe reports `none`.

The two shapes are complementary in practice. A pane fingerprint attests
within-turn movement that no turn boundary would show, and a turn-boundary
record attests movement a quiet pane would hide.

Declare an activity probe only for resources whose activity indicates session
progress: a chatty sidecar with an activity probe would mask a stalled agent,
so the author-declared-surface principle applies to probe placement too.

## Relationship to the workflow health cycle

A workflow's `[healthcheck]` table declares the sampling cycle — how often the
probes run, the stall threshold they are judged against, and the parent
re-notification interval. It names when health is observed; `[health]` names
what health means for one task.
