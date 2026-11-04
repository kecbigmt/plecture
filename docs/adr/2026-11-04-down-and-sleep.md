---
supersedes: 2026-09-08-down-session-resource-event-forwarding
---

# Separate full stop from capacity sleep

## Context

`plect down` formerly served both a deliberate stop and a temporary release
of runtime capacity. A child terminal push could restart a deliberately stopped
parent, while a resource event for a capacity-evicted member was forwarded to
an ancestor and could leave that member asleep indefinitely. The forwarding
decision in [the down-session resource event ADR](2026-09-08-down-session-resource-event-forwarding.md)
remains appropriate for a deliberate full stop. The cross-session terminal
push decision in the devbox D9 record supplied an unconditional wake; that
permission must follow lifecycle intent.

## Supersession scope

- [Decision](2026-09-08-down-session-resource-event-forwarding.md#decision):
  The rule to forward every down session's inbound events applies only to
  fully stopped sessions. A sleeping session requests its own wake.
- [Decision](2026-09-08-down-session-resource-event-forwarding.md#decision):
  The rule that only an explicit `up` resumes the origin applies only to
  fully stopped sessions. An inbound event can wake a sleeping session.

## Decision

Plain `plect down` is a full stop. It cleans run-scoped nodes and leaves the
session's `status` as `down`. Inbound events remain in its log and are forwarded
to the nearest live ancestor, but cannot restart it. `plect down --sleep`
performs the same cleanup and records a sleep intent. Every inbound event in
that sleeping session's log, including resource changes, replies, and child
terminal pushes, requests a wake. Outbound and internal events do not. The
sleeping session handles its own inbound events and does not forward them.
Finalized work cannot be woken by these events.

`sessions.sleep_since` is the sole nullable sleep marker. `NULL` means no
sleep intent; a timestamp means the session is sleeping. The latest
`plect.session.sleep` event distinguishes an idle sleep waiting for an inbound
event from a queued sleep waiting for a slot. A queued timestamp is the time
the wake request was first accepted and is kept across capacity retries. The
resident process attempts queued sessions in timestamp order. A request to
`up` past `max_up_children` succeeds as queued sleep: the CLI exits zero and
reports `accepted: sleeping (queue position N)`; JSON and MCP expose
`result: sleeping` and `queue_position`. When the runtime starts, the marker
is cleared and `plect.session.wake` records success. Repeated runtime wake
failures stop after the workflow's automatic repair attempt limit, or three
attempts when it has no positive limit, and `plect status` reports the reason.

Population capacity eviction uses sleep. Existing full-stop members remain
members, can still be destroyed when their resource disappears, and cannot be
re-admitted by an appearance. Population no longer uses inbound events to
re-admit members. A chain whose target already exists records an inbound kick
in that target's log; a full-stop target remains stopped, while a sleeping
target requests a wake. New chain targets follow the same capacity queue as
other `up` callers.

The one-time [migration procedure](../migrations/down-and-sleep.md) keeps
existing `down` sessions stopped, except those whose latest down was followed
by a `plect.workflow_population.down` event with reason `capacity`; those
receive a sleep marker. Other existing `down` sessions receive a stop event
so population admission recognizes their full-stop intent. That migration
event does not suppress forwarding of previously unread inbound events.

## Consequences

- Operators can stop an orchestrator without a child push restarting it.
- Capacity-evicted sessions resume themselves when an inbound event arrives
  and a slot becomes available.
- A queued request survives process restarts because its marker and intent
  event are durable.
- The additional nullable column requires a schema migration and the same
  procedure describes how to back up and verify existing data.

## Alternatives considered

- A second queue-reason column would duplicate the reason recorded in the
  sleep event and make ordinary transitions update two authorities.
- Reusing plain `down` for both intents would preserve the ambiguity that
  caused unsolicited wakes and missed reviews.
- Rewriting the older forwarding ADR's body would obscure the forwarding rules
  that remain in force for fully stopped sessions.
