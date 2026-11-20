---
supersedes: 2026-11-16-empty-status-idle-sleep
---

# Core owns the session inbox

## Context

An event may name the event it replaces in `supersedes`, and publishers set
it: the tick kick names the previous kick for the same task instance, and a
resource snapshot names the previous snapshot. The rule that follows from the
field is generic: an unprocessed event that another unprocessed event
supersedes is not delivered, and the events pending for a consumer are
delivered together.

That rule is applied by each consumer. Every runtime plugin that runs one
agent invocation per turn keeps a file queue per session. A channel
executable writes one file per event, and a batch script, copied into each
such plugin, claims the files, drops superseded entries, renders one turn,
counts failures, parks a batch after three, and moves unparsable files to a
quarantine directory. Review is the only thing that keeps the copies
identical.

Three problems come from that placement:

- A pane runtime has no queue, so it does not get the rule. Events that pile
  up while its session is down, asleep, or held for repair arrive one by one,
  superseded ones included.
- The file queue has failure modes that exist only because the queue is plugin
  files: a corrupt file, a leaked completion directory, a claim directory
  left by a crash.
- Retry counts, backoff deadlines and completion markers are consumer state on
  disk, so each new runtime has to rebuild them.

The session's event log and the dispatcher's delivery position already live in
core storage. A plugin's queue is a second copy of the unprocessed part of
that log, kept where core cannot see it.

## Supersession scope

- [Decision](2026-11-16-empty-status-idle-sleep.md#decision): The rule that an
  emitted resume event retains its priority ahead of queued events is
  replaced. Pending events are delivered in log order, and no event is
  reordered ahead of another.

## Decision

![The session dispatcher reads the event log past its delivery position and gives each binding the events its include selects. Push bindings receive one delivery per retained event, with superseded pending events omitted; the inbox binding receives one row per selected event in the core inbox_entries table. A headless worker takes the pending rows as one batch, in which superseded events are omitted, and acknowledges it after the turn; a batch taken again after a crash is the same batch, and a batch delivered three times without acknowledgement is parked with plect.channel.error. Plugin file queues and their batch script copies are superseded, as is the resume notice's priority. A byte-identity check over consumer copies, a position per consumer, take and acknowledge for push bindings, and an explicit failure report are rejected; a batch value for push channels is deferred.](assets/2026-11-20-core-session-inbox-decision.svg)

### The inbox

A session has one inbox in core storage. An inbox entry is a row that names
one event of the session's log by its sequence; the log stays the only copy
of the event. An entry exists from the moment the event is handed to the
inbox until its consumer acknowledges it or core parks it. An event is
processed when its entry is gone.

Events enter the inbox through a channel primitive, `type = "inbox"`. A
workflow binds it like any other channel, and the binding's `include` selects
what the runtime receives. The primitive has no action: for a matching event
the dispatcher inserts the entry instead of running a process or dialing a
socket. Every event one delivery pass selects enters in one transaction, so a
take sees the events that accumulated while a session was down together or
not at all. A workflow binds at most one channel of this type.

```toml
[runtime_inbox]
kind = "channel"
type = "inbox"
```

### Take and acknowledge

`plect inbox take <session>` returns the pending entries as one batch: a
batch id, the number of times the batch has been delivered, and the events in
log order. `--wait <duration>` blocks until an entry is pending or the
duration passes. With nothing pending the command prints nothing and succeeds.

`plect inbox ack <session> <batch>` removes the batch's entries.

- A take claims every entry pending at that moment. Entries that arrive later
  wait for the next batch.
- An entry is omitted from the returned events when another entry of the same
  batch names its event in `supersedes`. The newest event of a chain is
  returned. Omitted entries belong to the batch and leave with it. The
  decision reads event ids and the reference only.
- While a batch is unacknowledged, a take returns that batch again, with the
  same id and the same events, and counts one more delivery. A consumer that
  crashes between take and acknowledge therefore receives the batch again
  when it restarts, and no event is part of two acknowledged batches.
- A batch delivered three times without acknowledgement is parked by the
  next take: core appends one `plect.channel.error` per returned event,
  naming the binding, removes the entries, and claims the next batch.
- Acknowledging a batch that is not outstanding fails.

The delivery count is the whole retry state. A consumer that failed a turn
takes again and receives the count; it derives its backoff from that number
and stores nothing.

### Push bindings

For a `unix_socket`, `exec` or `shell` binding the dispatcher is the
consumer. Its pending events are the log past the session's delivery
position, and its acknowledgement is the outcome of the delivery attempt,
which advances that position as it does today. Before delivering, the
dispatcher omits, per push binding, every pending event that another pending event
selected by the same binding supersedes. It evaluates this at every drain, so
events that accumulated while the session was down, asleep, or held for
repair are judged together.

A push binding receives one delivery per retained event, in log order. Core
does not compose several events into one message for a push channel.

### Consumers that take

A runtime that runs one agent invocation per turn takes from the inbox: it
takes a batch, renders the returned events into one prompt, runs the
invocation, publishes its reply, and acknowledges. Rendering a batch into a
prompt stays in the plugin, because the prompt's shape belongs to the agent
it addresses. A consumer may cache its completed work for the outstanding
batch, keyed by the batch id; such a cache saves a repeated invocation and
is never the record of what is pending.

Pane runtimes keep their push channels and receive the retained events
through the dispatcher as described above.

[Headless runtimes take their turns from the session inbox](2026-11-20-headless-runtimes-take-from-inbox.md)
records how the shipped runtime plugins adopt this and what they remove.

### Schema and migration

One table is added. `events` and `event_cursors` do not change.

```sql
CREATE TABLE inbox_entries (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    channel TEXT NOT NULL,
    batch_id TEXT,
    deliveries INTEGER NOT NULL DEFAULT 0 CHECK (deliveries >= 0),
    PRIMARY KEY (session_id, sequence)
);
```

The schema migration is forward-only and adds an empty table. The
[migration procedure](../migrations/session-inbox.md) backs up storage first
and carries events still held in a plugin's file queue into the table.

## Consequences

- A session that comes back up after several superseding events receives
  only the newest, whatever its runtime. A runtime that takes receives
  everything pending as one turn.
- A new runtime of that kind implements take, render, acknowledge. It carries
  no queue, no retry counter and no parking logic.
- An unparsable queue file and a leaked completion directory cannot occur: an
  entry is a row that refers to a stored event.
- Parking is visible: it is a `plect.channel.error` in the session's log
  instead of a directory nobody reads.
- A turn interrupted by `down` or repair uses one of its batch's three
  deliveries, because core counts deliveries and cannot tell an interruption
  from a failure.
- Hand-over is at least once. The dispatcher remembers in memory how far it
  has handed over, so after it restarts with a delivery position that lags,
  an event the runtime already acknowledged can enter the inbox once more.
  Push deliveries have the same bound.
- The resume notice is not moved ahead of pending events. Events left pending
  when a session went down and the notice published when it comes back are
  one batch when the notice is in the log at the first take, and consecutive
  batches otherwise.
- A pane runtime with two distinct pending events still receives two
  deliveries, back to back.
- The storage schema changes, and sessions whose runtime kept a file queue
  need the migration procedure.

## Alternatives considered

- **Keep the consumer-side scripts and add a byte-identity check.** The check
  would be a standing check whose only job is to keep two copies of one rule
  equal. Pane runtimes would still not get the rule, the file queue would
  keep its failure modes, and every further runtime would add a copy.
- **A position per consumer instead of entry rows.** A consumer position with
  one outstanding range needs no row per event. It needs a second authority
  for where a binding's position starts at session creation, at a rebase that
  adds the binding, and at migration, and the take command would have to
  re-evaluate the binding's `include`. With rows, selecting an event stays
  the dispatcher's single decision.
- **Take and acknowledge for push bindings too.** A position per push binding
  would stop one failing channel from holding the others. It rewrites the
  dispatcher's hold and health accounting with no consumer asking for it, and
  a push delivery has no interval between delivery and acknowledgement for a
  claim to protect.
- **A batch value for push channels.** A channel could read a batch root and
  compose one message from several events. That is a new configuration
  construct in every runtime channel, and a pane already queues input that
  arrives during a turn. It is deferred until a pane runtime shows that
  back-to-back deliveries cost separate turns.
- **Acknowledge a pane's delivery at turn end.** Holding later events until a
  hook reports the end of the turn would stall the inbox when a turn never
  ends, and would withhold a message a person sends to steer a running turn.
- **Count reported failures instead of deliveries.** A consumer that crashes
  reports nothing, so a batch that kills its consumer would be delivered
  forever. Counting deliveries bounds both cases with one number.
- **A priority property for the resume notice.** No consumer needs an order
  other than log order, and a property with one user would be a rule about
  one event type under a generic name.
