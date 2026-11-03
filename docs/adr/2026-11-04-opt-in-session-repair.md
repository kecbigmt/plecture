---
supersedes: 2026-09-06-runtime-failure-model
---

# Opt-in repair of sessions intended to be up

## Context

The runtime failure model makes plain `plect up` a convergence operation but
keeps periodic health evaluation observational. That prevents a health cycle
from changing external resources without operator or workflow intent. It also
leaves an unattended session with a dead runtime unhealthy until a caller runs
`plect up`. A root session's escalation stays in its own event log.

The session's persisted `status` records lifecycle intent: successful `up`
sets it to `up`, and `down` sets it to `down` before teardown. The derived run
state answers a different question: whether a run-scoped node is produced. A
session may therefore have `status = up` while its run is down. A deliberate
`down`, including sleep, must never be undone by the health cycle.

## Decision

The health cycle may call plain `service.Up` only for a session whose frozen
workflow opts in and whose persisted `status` is `up`. This narrows the earlier
rule that health only reports failures; the runtime failure model's other
liveness, node-result, and lifecycle decisions remain in force. The workflow's
intent and the session's persisted lifecycle intent are both required before
periodic observation may cause a mutation.

The workflow-level `[health]` table carries `auto_repair_attempts`, a
non-negative integer with default `0` (disabled). It is distinct from an
effect's `[health]` probe table: the workflow owns permission to repair its
session, while effects define the facts used to evaluate liveness. The existing
workflow `[healthcheck]` table continues to set sampling cadence. A negative,
fractional, or non-integer value is a load error. No `[repair]` table is added
because repair is a response to the health cycle's observation.

```toml
[review_agent]
kind     = "workflow"
resource = "pull_request"

[review_agent.health]
auto_repair_attempts = 3

[review_agent.healthcheck]
period = "5m"
```

On each cycle, `plect serve` considers only sessions that still exist, are
neither finalized nor destroyed, have `status = up`, and have an opt-in limit
greater than zero. It attempts repair when a completed health evaluation names
an `alive` action's non-zero exit or a failed or missing current-plan node. It
also attempts repair when `status = up` but derived run state is `down`; that
case cannot acquire a complete-plan health verdict. Stalled activity, an
undeclared probe, and other health outcomes do not authorize repair.

An executable `alive` action has a 30-second timeout by default. Timeout,
launch failure, unresolved inputs, and invalid probe configuration mean the
probe could not be evaluated; they do not establish that the surface is gone.
The cycle records `skipped_probe_error` and retries observation at the next
cycle without calling `Up` or consuming an attempt. The same distinction
applies when `Up` verifies a produced node for reuse, so an indeterminate
probe never causes cleanup or reconstruction.

The serve process admits at most one repair at a time across all sessions. It
acquires the target session's lifecycle lock, then re-reads intent and
eligibility before calling existing `service.Up` without force recreation. A
concurrent human `down`, finalize, destroy, or healthy observation can thereby
cancel a queued repair. Sessions with `status = down`, including sleeping
sessions, are never admitted. Repair does not wake a deliberately down session.

The attempt budget counts `plect.session.repair` events with
`result = started` since the latest successful human `plect up` or observed
healthy state. A successful human `up` or healthy observation records a reset
boundary in the same session event stream when a repair streak exists; it does
not need a new state column. A successful automatic `Up` does not by itself
reset the budget: a later healthy observation must prove recovery. A failed
automatic `Up` also consumes its started attempt. When the count reaches the
configured limit and the session still needs repair, the cycle emits
`suspended` once for that streak, makes no further `Up` calls, and reports
`repair_suspended` with the reason and attempt limit in `plect status`.
Healthy observation or successful human `up` clears suspension and starts a
new budget.

Every repair lifecycle event is appended to the affected session's log with
type `plect.session.repair`, source `plect`, and internal direction. Its
metadata is:

| Key | Value |
|---|---|
| `trigger` | `health` for a liveness or structural failure, `run_down` for `status = up` with run down, `manual_up` for a human reset boundary, or `healthy_observed` for a health reset boundary. |
| `attempt` | Decimal ordinal in the current streak, beginning at `1`; `0` for a reset boundary or an unevaluable probe. |
| `result` | `started`, `succeeded`, `failed`, `suspended`, or `skipped_probe_error`. |
| `reason` | The observed failing node and cause, run-down condition, probe error, or suspension limit. |

One `started` event is written before each automatic `Up`; a corresponding
`succeeded` or `failed` event records its result with the same `attempt`.
`suspended` and `skipped_probe_error` do not increment the count. A successful
human `up` or healthy observation with an active repair streak writes
`succeeded` with `attempt = 0` and the corresponding reset trigger. This marks
the reset boundary without claiming an automatic attempt. The event stream is
the attempt authority; session health is the observation authority. Stream
order resolves a healthy observation and a repair event occurring within the
same timestamp precision.

```json
{
  "type": "plect.session.repair",
  "source": "plect",
  "direction": "internal",
  "summary": "repair attempt 2 started",
  "metadata": {
    "trigger": "health",
    "attempt": "2",
    "result": "started",
    "reason": "agent: alive exited non-zero"
  }
}
```

## Consequences

An opted-in workflow can recover an unhealthy unattended session while the
session still records intent to be up. Periodic liveness probes become a
mutation gate, so errors that prevent evaluation must remain distinct from
observed non-zero exits. Repeated failures are bounded and visible through
events and status. Repair uses the existing `Up` node walk, preserving its
verify-before-skip behavior and its `plect.node.result` account of each node.

The implementation adds workflow configuration, probe timeout handling,
repair scheduling, event-derived budget accounting, status reporting, and
regression tests. Once fault-injection scenarios S1 and S2 pass with automatic
repair, the Docker fault harness runs nightly as well as by manual dispatch.
This is the one standing check added by the repair work: without it, a later
change could silently break recovery after a container restart.

## Alternatives considered

Keeping health strictly observational leaves opted-in unattended sessions with
no actor to invoke the already safe plain `up` convergence operation. Using
derived run state alone as intent would also wake deliberately down sessions
and miss a failed setup that produced no run-scoped node.

A separate `[repair]` table would split one health-cycle policy across two
workflow sections without a distinct consumer. A stored attempt counter would
create a second authority for the same repair history already recorded in the
event log. Counting only failed attempts would permit an unbounded sequence of
started repairs whose success never yields a healthy observation.
