# Headless runtimes take their turns from the session inbox

## Context

The `claude` plugin's `headless_runtime` and the `codex` plugin's
`exec_runtime` run one agent process per turn. Each keeps a queue directory
per session. The plugin's channel (`headless_delivery`, `exec_delivery`) runs
an enqueue executable that writes one file per event, and the worker drains
the directory through its plugin's copy of `headless-batch.sh`: claim into
`.inflight`, drop superseded entries, render one prompt, count failures in
files, park a batch as `.failed-<id>`, complete it through `.completed-<id>`,
and move an unparsable file to a quarantine directory.
`plugins/headless-queue-conformance.sh` runs one test suite against both
copies.

[Core owns the session inbox](2026-11-20-core-session-inbox.md) moves the
pending events, the supersession rule, the batch, redelivery and parking into
core. This record states what the two plugins do with it.

The `claude` plugin's worker also runs a `channel-server queue` bridge: a
socket that turns each message from an adapter into a queue file. It was
added for an adapter that dialed a runtime's socket. The shipped adapters now
publish events to a session through core.

## Decision

![A headless worker takes a batch from the core session inbox, renders the events into one prompt with the runtime's message_envelope, runs one agent process, keeps the reply for that batch id, publishes it as plect.message, and acknowledges the batch. The queue directory, the enqueue executables, both headless-batch.sh copies, the conformance script and the channel-server queue bridge are removed. Keeping the envelope on the channel and keeping the bridge as an event publisher are rejected.](assets/2026-11-20-headless-runtimes-take-from-inbox-flow.svg)

### Channels

`headless_delivery` and `exec_delivery` become `type = "inbox"` channels with
no inputs. A workflow keeps its binding and its `include`, and drops the
binding's `inputs` table.

```toml
[[my_workflow.event.channel]]
name    = "runtime"
uses    = "official.claude.headless_delivery"
include = ["plect.instruction", "user.emit"]
```

### Worker loop

Each worker repeats one sequence:

1. `plect inbox take` with a wait. Nothing pending means wait again.
2. A batch delivered before waits two seconds per earlier delivery.
3. Write the `working` status. A failed write is retried without taking
   again, so it does not use one of the batch's deliveries.
4. Render the batch's events into one prompt: the instruction to answer every
   entry once, then each event numbered in log order with its id, type and
   source, formatted by `message_envelope`.
5. Run one agent process, unless one already exited successfully for this
   batch id.
6. Publish the reply as `plect.message`, with the batch id in its
   `message_id`.
7. `plect inbox ack`.

A failed agent process or a failed publication ends the pass without an
acknowledgement, and the next take returns the same batch. The reply file, a
completed marker and a published marker are kept in the runtime's state
directory under the batch id and removed after the acknowledgement; they
exist so a redelivery does not pay for a second agent run or post the reply
twice. The completed marker is written only after the agent process exits
successfully: an agent can report its reply before it exits, so the reply
file alone does not show that the turn finished.

### Inputs and outputs

`message_envelope` moves from the channel to the runtime effect, because the
worker renders the prompt and the effect launches the worker. Its
placeholders and default do not change. `enqueue_timeout` and the `queue_dir`
channel input and effect output are removed.

### Removed

- `claude-headless-enqueue`, `codex-exec-enqueue`, both `headless-batch.sh`
  copies, and `plugins/headless-queue-conformance.sh`.
- The queue directory with `.inflight`, `.failed-<id>`, `.completed-<id>` and
  the quarantine directory.
- The `channel-server queue` mode and the `socket_path` output of
  `headless_runtime`. A sender reaches a headless session by publishing an
  event to it.

### In-flight queue contents

The [migration procedure](../migrations/session-inbox.md) runs with the
resident process stopped and storage backed up. For each session with a
queue directory it inserts an inbox entry for every pending or claimed file
that carries an event id. Files without an event id, which only the bridge
wrote, and parked or quarantined files are listed for the operator and left
in place. Sessions are then rebased onto the new declarations with a restart.

## Consequences

- Both workers lose their batch, retry and parking code; each keeps the
  prompt rendering and the agent invocation that differ between them.
- A leaked `.completed-<id>` directory and an unparsable queue file cannot
  occur, so neither needs a cleanup or a quarantine.
- A parked batch is reported as `plect.channel.error` in the session's log.
- Workflows that bind `queue_dir`, `enqueue_timeout` or `message_envelope` on
  these channels, or read `socket_path` from `headless_runtime`, must be
  edited as the migration procedure describes.
- The two workers render a batch with separate code. The rendering is prompt
  text, which each plugin owns.

## Alternatives considered

- **Keep `message_envelope` on the channel.** Core would have to carry a
  binding's resolved inputs to the consumer with each batch, a new part of the
  take contract for one formatting string.
- **Keep the bridge and have it publish events.** The bridge would need an
  event type that the binding's `include` selects, which it cannot know, and
  no shipped adapter dials a runtime socket any more.
- **Drain a leftover queue directory in the new worker.** The worker would
  carry the old file format until the last pre-upgrade session is gone. A
  one-time migration moves the entries instead.
