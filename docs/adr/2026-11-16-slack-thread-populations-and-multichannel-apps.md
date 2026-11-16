---
supersedes: 2026-11-15-slack-plugin-delivery
---

# Slack thread populations and multichannel Apps

## Context

The bot-resource subscriber provides a standing recipient for mentions in an
unbound thread. A deployment also needs to start one population member per
mentioned thread, let later mentions reach that member, and apply the ordinary
population expiry and capacity policies. The earlier Slack delivery decision
deferred this path. Its one-channel bot declaration also causes a shared App
used in several channels to reject or misroute messages: Slack Socket Mode
may deliver an App event to any of that App's connections, without channel
affinity.

The resident adapter has no HTTP listener or subscriber registry. Core owns
both resource subscriptions and population declarations. A query stream's
connection state cannot decide routing because it may disconnect while the
declaration is still active.

## Supersession scope

- [One adapter and outbound calls](2026-11-15-slack-plugin-delivery.md#one-adapter-and-outbound-calls): the one-channel bot declaration and one connection per bot entry.
- [Mention routing](2026-11-15-slack-plugin-delivery.md#mention-routing): the fallback for an unbound mention in a population-covered channel, removal of the thread `query.subscribe`, and deferral of per-thread creation.

## Decision

![One App connection accepts mentions in several configured channels. A core query snapshot declares population-covered channels independently of stream connectivity. For a covered unbound mention, the adapter persists it, streams an appearance, waits for a thread subscription, and publishes the mention. Other allowed channels retain the bot-resource fallback.](assets/2026-11-16-slack-thread-populations-and-multichannel-apps-routing.svg)

Each bot declaration has a nonempty, unique `channel_ids` array. One App
declaration owns one Socket Mode connection and one set of user and denial
policies. Channel IDs cannot overlap across declarations, and duplicate App
credentials or authenticated identities are rejected. The adapter selects an
outbound App by channel ID. Recovery scans and positions remain per App and
channel.

The Slack thread resource again declares `query.subscribe` and a schema for
thread appearance items. A workflow population names the thread resource,
selects `uses = ["subscribe"]`, declares `channel_ids` in its query, and uses
the existing `expire_after`, `auto_down`, `auto_destroy`, and capacity rules.
Its query action connects to a private Unix-domain stream on the resident
adapter. The service and query action receive the same operator-selected
socket path. The action emits the existing query item shape and opens no
Socket Mode connection.

Core sends each service a complete snapshot of active subscribe populations
whose query action uses that service's executable, before Socket Mode starts.
It sends replacement snapshots on configuration reload. This generic
protocol carries population identity and validated query inputs; core does
not interpret Slack channels. The adapter derives coverage from the query's
`channel_ids`, rejects overlapping populations for an App, and retains the
last valid snapshot when a replacement is invalid. A stream disconnect does
not change coverage.

An allowed mention first goes to any existing thread subscriber. When that
publish delivers to nobody, a population-covered channel stores the mention
durably, emits an appearance, and keeps it pending until a thread subscription
is visible. The adapter then publishes `user.emit` with the fetched thread
transcript and records success after a positive delivered count. A failed
publish or missing subscriber leaves the mention pending for replay. The
population's stable resource identity admits one member per thread; a later
mention uses its existing subscription. An unbound mention in an uncovered
allowed channel uses the bot-resource fallback; zero delivery there is logged
as a drop. Removing coverage resolves pending mentions under the replacement
routing rule with an explicit log.

## Consequences

The service keeps pending mentions in its private data directory and a local
feed socket only for query actions. It still keeps no subscriber ledger or
HTTP listener. The core protocol grows a provider-neutral query snapshot so
the adapter knows the declared channel coverage before events arrive and
while a query stream is disconnected. The bot resource keeps
`max_subscribers = 1` and may coexist with thread populations in other
channels.

The `channel_id` service key is removed. Deployments use the
[backup-first migration](../migrations/slack-multichannel-thread-populations.md)
to convert it to `channel_ids` and consolidate entries for one App. A crash
after core accepts a publication but before the adapter records success can
cause a duplicate on replay; no generic idempotency contract is added.

## Alternatives considered

### One routing mode per bot

A bot-wide mode cannot serve per-thread operations channels and a standing
bot subscriber in another allowed channel. Channel coverage already belongs
to the population query, so a second mode would duplicate policy.

### Infer coverage from connected query streams

A temporarily disconnected stream would send a covered mention to the bot
subscriber. The durable query declaration, not the connection, determines
the recipient.

### Repeat one App declaration per channel

Slack may deliver an App event to any active connection. Repeating credentials
per channel makes each connection capable of receiving a different channel's
event and applying the wrong allow check.

### Resident dispatcher session

A standing session subscribed to the bot resource could run `plect up` for
each mention without a plugin or core change. It requires an always-on agent
in the routing path, moves per-thread lifecycle out of population
declarations, and must hand the triggering mention and transcript to the new
session itself.

### Generic publication idempotency key

A key persisted atomically with each session event would close the crash
window, but adds a core contract for a failure mode already documented by
restart recovery. The normal-operation handoff is the required guarantee.
