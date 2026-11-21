---
superseded_by: 2026-11-20-core-session-inbox
---

# Use the durable empty status for idle sleep and resume decisions

## Context

The status event log already records an empty `plect.status_message` at a
completed turn and a nonempty status when a turn starts. Population capacity
eviction uses the latest explicit clear after the last activation as evidence
that a session may release capacity. Run-scoped setup publishes
`plect.session.resumed` on every recreated run, including one that merely
wakes after an idle sleep. A new idle policy needs to release a worker's
capacity without telling it to inspect an interrupted turn when no turn was
interrupted.

An unmerged session idle policy draft in PR #574 proposes
`[<workflow>.session].idle_down_after` for automatic full down. Its separate
session-level destroy policy and population field removals address different
decisions.

## Decision

![The empty status a runtime writes at turn end is the one idle fact. The idle-sleep deadline starts at that status in the current run and elapses after sleep_after_idle with no newer inbound; dispatched sessions then sleep and release capacity, while operator-created top-level sessions, sessions with no completed turn, and workflows without the duration never sleep automatically. The resume decision reads the previous run's last status before setup and ignores the waking inbound: an empty status suppresses plect.session.resumed, while a nonempty status or a runtime that never writes status keeps it. The PR #574 draft's idle_down_after is superseded; its destroy policy and population field removals are not taken over.](assets/2026-11-16-empty-status-idle-sleep-decision.svg)

The workflow's optional `[<id>.session].sleep_after_idle` is a positive
duration. A resident sweep applies it only to dispatched sessions: a session
with a parent session or population provenance. A directly created top-level
session, including one started with bare `plect up`, never sleeps automatically.
The sweep sleeps an eligible up session when its latest status is an
explicit clear written after the current run began, no inbound event is
newer, and the duration has elapsed. The sweep uses the existing
`down --sleep` transition, including its capacity release and inbound wake.
An inbound event accepted during cleanup requests wake after that cleanup.
An absent duration disables automatic sleep. A session that has completed no
turn in the current run has no qualifying clear.

The empty `plect.status_message` remains the single durable idle fact. Core
adds no idle event, command, run identifier, or session column. A headless
worker verifies that its nonempty `working` status write succeeds before it
starts each turn; on failure it leaves the turn queued and reports the error.
Existing turn-end hooks write the empty status. Interactive runtimes that
write an empty status participate in resume suppression too.

Before setting up a new run, `Up` reads the latest status written after the
previous run's setup. It
publishes `plect.session.resumed` only when a run-scoped node is recreated and
that status is absent or nonempty. The waking inbound event is not part of
this decision; it is part of the sleep deadline decision. An emitted resume
event retains its priority ahead of queued events and its `repair`, `wake`,
or `up` trigger metadata.

This decision supersedes PR #574's draft `idle_down_after` decision. It does
not adopt that draft's session-level destroy policy or remove population
`auto_down` and `auto_destroy`; those remain separate decisions. The sleep
lifecycle follows [the down and sleep decision](2026-11-04-down-and-sleep.md).

## Consequences

- Idle headless and interactive sessions do not receive an interruption
  instruction after a routine restart.
- A worker with a failed start-of-turn status write does not begin the turn,
  so a prior empty report cannot suppress an instruction for an interrupted
  turn.
- Automatic sleep releases `max_up_children` capacity and keeps the existing
  inbound wake behavior.
- An inbound that wakes a slept session but is not delivered to the worker
  because its type is outside the runtime channel's `include` or it has no
  text starts a run with no completed turn. The session holds its
  `max_up_children` slot and does not sleep again until a delivered event
  produces a turn and a fresh clear that starts a new idle-sleep wait.
- Directly created top-level sessions stay up even when their workflow declares
  an idle-sleep duration; parented and population-admitted sessions use it.
- No storage schema change or data migration is required.

## Alternatives considered

- A separate activity event or a session column would create another durable
  answer to whether a turn ended idle, overlapping the existing status log.
- A headless-only idle confirmation would exclude interactive runtimes that
  already write an empty status at a completed turn.
- Reusing the population eviction predicate for resume suppression would
  emit a resume instruction after every inbound wake, because the waking
  event is newer than the clear that preceded sleep.
