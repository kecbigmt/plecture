# Channels

A channel definition binds an event delivery to a primitive. Trusted config
fixes the primitive and the message shape; a workflow supplies only the
author-declared parameters. Event data never chooses what runs.

Channels are trusted plugin, machine-owned, or selected trusted-project
configuration: an `exec` or `shell` channel runs a process, so an untrusted
repository checkout may not declare one.

## Primitives

| `type` | Delivery |
|---|---|
| `unix_socket` | Dial `path`, write `body` as one framed message. |
| `exec` | Run an exec action. |
| `shell` | Run a shell action. |
| `inbox` | Hand the event to the session inbox. Core runs nothing. |

<!-- fixture: channels/unix-socket.toml -->
```toml
[delivery]
kind = "channel"
type = "unix_socket"
path = { from = "inputs.path" }
body = { json = { from = "event" } }

[delivery.input_schema]
path = { type = "string", required = true }
```

An `exec` channel names a plugin-owned executable through `bin`, or a literal
OS command through `command` — see [`actions.md`](actions.md).

A `shell` channel exists for delivery that is genuinely imperative: splitting a
keystroke burst, polling a readiness predicate, retrying with backoff. Its
`script` is literal and its `bind` table carries the event data and
capabilities it needs, so the event is never part of the command.

<!-- fixture: channels/terminal-capability.toml -->
```toml
[terminal_submit]
kind = "channel"
type = "shell"
script = '''
set -u
sh -c "$send_text" terminal-send-text "$message"
sleep 1
sh -c "$send_keys" terminal-send-keys Enter
'''
timeout = "45s"

[terminal_submit.bind]
send_text = { terminal = "send_text" }
send_keys = { terminal = "send_keys" }
capture   = { terminal = "capture" }
message   = { expr = "'[' + event.type + '] ' + (event.body != '' ? event.body : event.summary)" }
```

An `inbox` channel declares no delivery of its own: the runtime the workflow
launches takes the events from the [session inbox](#session-inbox).

<!-- fixture: channels/inbox.toml -->
```toml
[runtime_inbox]
kind = "channel"
type = "inbox"
```

## Selecting events

A channel definition only fixes delivery; a workflow's own
`[[<id>.event.channel]]` binding (see [`workflows.md`](workflows.md#event-channels))
selects one and lists the event-type globs it relays with `include`:

```toml
[[coding.event.channel]]
name    = "runtime"
uses    = "delivery"
include = ["plect.instruction", "plect.node.result"]
```

An `include` entry matching `plect.instruction` also relays
`plect.session.resumed`. This rule applies to any channel with that include,
including a channel that delivers to a mirror rather than a runtime.

A channel's own delivery failure produces a `plect.channel.error` event
(`events.md`), which `include` can select like any other event type — a
workflow can relay one channel's failures to a second, independent channel.
Delivery of a `plect.channel.error` skips only the channel whose failure it
records, so that channel never loops on its own failure; a `plect.channel.error`
recording the failure of relaying another `plect.channel.error` is never
delivered to any channel, so two channels erroring at each other cannot
ping-pong.

## Session inbox

A session has one inbox. It holds, in core storage, a reference to every event
its `inbox` binding selected and its runtime has not acknowledged. All events
one delivery pass selects enter together.

`plect inbox take <session>` returns the pending events as one batch: a JSON
object with `batch` (the id to acknowledge), `deliveries` (how often the batch
has been handed out), and `events` in log order, with superseded events left
out ([`events.md`](events.md#supersession)). `--wait <duration>` blocks until
an event is pending or the duration passes. With nothing pending the command
prints nothing and succeeds.

`plect inbox ack <session> <batch>` removes the batch's events from the inbox,
the omitted ones included. It fails for a batch that is not outstanding.

- A take claims every event pending at that moment. Events that arrive later
  form the next batch.
- Until a batch is acknowledged, a take returns the same batch again, with the
  same id and events, and counts one more delivery. A runtime that stops
  between take and acknowledge receives the batch again when it restarts.
- A batch delivered three times without an acknowledgement is parked by the
  next take: one `plect.channel.error` per returned event names the binding,
  the events leave the inbox, and the take returns the following batch.
- Take and acknowledge write to one session. Both fail for a session that does
  not exist and for one outside the calling process's session guard
  (`PLECT_SESSION_GUARD`).

## Parameters

`[<id>.input_schema]` declares the channel's parameters per key: a `type`, a
`required` flag, and an optional `default`. This is deliberately not the full
JSON Schema document effects and resources carry — only a channel input's
presence is checked before delivery, and a `default` is what makes an optional
parameter usable at all.

## Timeout

`timeout` bounds one delivery attempt. It reads author-declared parameters
only; event data is excluded, so the event being delivered can never choose its
own deadline. An empty `timeout` leaves the deadline to the caller's retry
policy.

## Validation rules

- `type` is `unix_socket`, `exec`, `shell`, or `inbox`.
- An `inbox` channel declares `kind` and `type` only.
- A workflow binds at most one `inbox` channel.
- A `unix_socket` channel declares `path` and `body`, and no process fields.
- An `exec` channel declares exactly one of `bin` and `command`.
- A `shell` channel declares `script`, containing no interpolation.
- `command` is never a computed value.
- `timeout` projects `inputs.*` only.
- A terminal capability requires some effect in the plan to declare that verb.
