---
supersedes: 2026-09-05-standing-session-dispatch
---

# Reclaim population capacity from stalled sessions

## Context

The [standing-session dispatch decision](2026-09-05-standing-session-dispatch.md#capacity-pressure-policy)
permits capacity reclamation only after a runtime explicitly clears its status.
A runtime that stops making progress without clearing its status can therefore
hold a virtual-root slot indefinitely. The session health checker already
persists a `stalled` verdict and the time of its check. That verdict identifies
an execution surface that is present but has stopped showing activity; an
`unhealthy` verdict identifies a failed surface and does not carry the same
meaning.

## Supersession scope

- [Capacity-pressure policy](2026-09-05-standing-session-dispatch.md#capacity-pressure-policy):
  Replace the requirement that every candidate explicitly report idle, the
  exclusion of runtimes that never report idle, and the single last-activity
  ordering rule.

## Decision

![At a full virtual-root cap, the population capacity coordinator receives up population members with auto_down enabled. One path accepts an explicit status clear no earlier than activation. A second path accepts a persisted stalled health verdict checked no earlier than activation when no status event follows that check. The stalled path sorts by activation time; the clear path sorts by clear-event time. Both feed the oldest-first candidate order and ordinary session down, followed by another admission attempt. The earlier explicit-idle-only gate is superseded.](assets/2026-11-21-stalled-population-capacity-decision.svg)

The coordinator still considers only up, parentless, population-owned members
whose entry permits `auto_down`. For each member, activation is the latest of
session creation, accepted resource appearance, and inbound session event.

A member is eligible when its latest durable status event is an explicit clear
at or after activation. It is also eligible when its persisted health state is
`stalled`, its last check is nonzero and at or after activation, and no durable
status event is later than that check. A later status event blocks the stalled
path until another health check establishes a fresh stalled verdict. A later
activation likewise requires a new clear or stalled check. A member with no
clear and no qualifying stalled verdict remains ineligible. In particular,
an `unhealthy` verdict does not qualify through the stalled path.

Eligible members sort by oldest activity and then session name. On the clear
path, activity is the clear event's time. On the stalled path, activity is the
activation time; the health-check time and any earlier status-event time do
not enter the sort key. The coordinator takes a candidate down through the
ordinary lifecycle and retries admission under the existing capacity policy.

## Consequences

A stalled member can release capacity even if its runtime never reports idle.
Activation and a later working status protect a member from being selected on
an obsolete stalled check. A later check can make it eligible again. The
selection order may put a recently checked stalled member ahead of a member
that explicitly cleared earlier, because the stalled member's sort key is its
activation time.

## Alternatives considered

- Require an explicit clear for every candidate. A stalled runtime that
  cannot report status would keep its slot indefinitely.
- Treat every non-healthy verdict as eligible. An `unhealthy` verdict can mean
  a dead execution surface, so it is not evidence of stalled work.
- Sort stalled candidates by health-check time. A repeated check would move
  a member to the back without any new activation or runtime activity.
