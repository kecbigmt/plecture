---
supersedes: 2026-11-15-slack-plugin-delivery
---

# Slack working status with complete replies

## Context

Each channel delivery runs a separate action process. A live Slack stream
needs its stream timestamp and recipient across message deltas, but no shared
stream state is available to those processes. The resident adapter could own
that state, at the cost of adding a new delivery contract between actions and
the service. Slack's assistant working status can indicate progress without
that contract.

## Supersession scope

This decision replaces only the streaming part of
`2026-11-15-slack-plugin-delivery`:

- `### One adapter and outbound calls`: the decision that streaming calls the Slack Web API directly.

## Decision

![A status message starts Slack's working display. A complete reply is posted in one action, which clears the status after a successful post. An empty status message also clears the display when a turn ends without a reply. Live delta streaming through the resident service is deferred.](assets/2026-11-15-slack-working-status-delivery.svg)

The `stream` channel posts one complete `plect.message` as a thread reply.
Nonempty `plect.status_message` events set Slack's assistant status to
`processing` before the complete reply. A successful reply post clears the
status. An empty status event clears it at turn end, including turns with no
reply. No action retains stream state between deliveries.

## Consequences

Slack shows its working display while a turn is in progress and a complete
reply when one is available. The display clears after the reply or at turn
end. Incremental answer text does not appear in the Slack thread.

## Alternatives considered

### Live streaming through the resident service

The service could retain stream timestamps and recipients and accept delta
deliveries from actions. That requires a new cross-process contract and is
deferred until live previews have a concrete consumer need.
