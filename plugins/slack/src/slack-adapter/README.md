# slack-adapter -- Slack adapter for channel-server

Creates and posts to Slack threads over HTTP. When an app token is configured
it also keeps a Socket Mode connection to Slack and routes messages to/from
Claude Code sessions (channel-server) over a Unix socket.

## Architecture

```
plect up
  ├─ POST slack-adapter:7890/threads         → create a Slack thread
  ├─ tmux + claude (run-scoped tasks)      → start claude, resolve socket_path
  └─ POST slack-adapter:7890/subscribe       → register thread_ts → socket_path with the broker

       channel-server (MCP, one per session)
            │ Unix socket (CHANNEL_SOCKET_PATH)
            ▼
       slack-adapter (long-running broker)
            │ in-memory map: thread_ts → {channel_id, socket_path, session_name, since, delivered_through}
            │ Slack Socket Mode (1 connection)
            ▼
          Slack
```

slack-adapter never reads plect's state.json. Every subscription is registered
and torn down explicitly through the HTTP API.

## Configuration

### config file (`~/.config/slack-adapter/config.toml`)

```toml
slack_bot_token = "xoxb-..."
slack_app_token = "xapp-..." # optional; enables Socket Mode inbound relay
channel_id = "C..."          # optional default for requests without channel_id
listen_addr = "127.0.0.1:7890"
allowed_user_ids = ["U..."]
deliver_full_thread = false # optional; default is root + delta on @-mention
status_ttl = "15m"          # optional; logs an overdue processing state
on_unbound_mention = "/path/to/dispatch-command" # optional; see below
```

`on_unbound_mention` can also be set via `SLACK_ADAPTER_ON_UNBOUND_MENTION`,
subject to the same config-file-wins-over-env-var rule as the credential
fields above.

Outbound-only operation requires only `slack_bot_token`. Requests that omit
`channel_id` require the optional configured default.

### Thread status

The `status` channel maps a nonempty `plect.status_message` to
`agents.sessions.setStatus(processing)` and the runtime's empty waiting
report to `active`. The event's wording remains available to other event
consumers; Slack renders its own “Working…” text. An individual reply,
permission prompt, or final stream chunk does not end the turn. The session
dispatcher delivers earlier message events before the waiting event, and
the adapter serializes delivery and status calls per channel and thread.
Native stream completion explicitly requests `session_status=processing`
until the waiting event arrives. If a turn posts no answer, the waiting
event still sets `active`.

`status_ttl` logs an overdue processing state without changing it. Slack
times out processing after one hour. A late waiting event with a different
`turn_id` is ignored; events without a turn ID use generation-only handling
and produce a diagnostic.

`status_loading_messages` (the old receipt-time shimmer's text) is a
retired config key: `ValidateStartup` fails startup with an error naming it
if a `config.toml` still sets it, rather than silently ignoring a setting
that no longer does anything. See
`docs/migrations/slack-status-loading-messages-migration.md` to remove it.

The Slack app must be declared as an agent. The existing bot token already
has `assistant:write`, and `chat:write` remains required. Agent declaration
is an owner action; see
`docs/migrations/slack-agent-session-status.md`. The adapter does not
subscribe to `agent_session_stopped`, so Slack may return a
`missing_agent_session_stopped_event_subscription` warning and shows no
interactive stop button. This API migration does not switch `agent_view`.

When Socket Mode is enabled, an `app_mention` in a subscribed thread publishes
one inbound `user.emit` to the bound `session_name`. The event body is an
ordered Slack transcript with display names, message times, and text. By
default each delivery includes the root message plus only replies after the
thread's last successful delivery watermark, followed by the mentioning
message. Set `deliver_full_thread = true` to send the full thread on every
mention. Empty `allowed_user_ids` allows any channel member to drive this
mention path; setting it restricts app mentions to the listed Slack users.

### `on_unbound_mention` hook

An `app_mention` that resolves to no subscription is normally logged and
dropped (`app mention skipped: unbound thread`). When `on_unbound_mention` is
set, slack-adapter instead runs that command once, with a JSON document on
stdin describing the mention, and logs its exit status. `allowed_user_ids`
is applied first, so a mention from a disallowed user never reaches the
hook. A top-level mention (no `thread_ts`) is treated as the root of a new
thread: `thread_ts` in the payload equals the mention's own `ts`. A mention
in a *bound* thread is unaffected — the hook only fires where today's code
drops the event.

```json
{"channel_id": "C...", "thread_ts": "1788222413.916339", "ts": "1788224629.760139", "user": "U...", "text": "<@U...> ...", "permalink": "https://<ws>.slack.com/archives/C.../p..."}
```

slack-adapter does not wait for anything beyond the command's exit status
(logged) and never retries. Everything else — which channels to honour,
which workflow to start, rate limits — is the command's job, not this
plugin's: it is deployment policy, and the adapter stays a source-agnostic
relay. A typical command runs
`plect up <permalink> --workflow <wf> --inputs '{"mention_ts": "<ts>"}'`,
and the workflow's `slack_subscribe` node (with `catch_up_through =
mention_ts`) brings the thread's history, including the triggering mention,
to the runtime.

### `subscribe unbound-mentions` (query.subscribe action)

```
slack-adapter subscribe unbound-mentions --base-url <url> --channel-ids '["C...", "C..."]' \
  [--user-ids '["U...", "U..."]'] [--denied-user-message '<text>'] [--denied-channel-message '<text>']
```

A separate invocation of the `slack-adapter` binary — not the resident
service — that connects to a *running* resident adapter's `GET
/unbound-mentions` feed (never opening a second Socket Mode connection
itself), filters to `--channel-ids`, and writes one JSON item per line to
stdout for each match, in the item shape `GET /unbound-mentions` documents
below.

`--user-ids` restricts which users may start a session: when given and
non-empty, only mentions whose `user_id` equals one of the listed Slack user
IDs (exact match; no email or display name) are emitted. Omitted or empty,
every user passes.

A mention that is not emitted is dropped silently unless a reply is
configured. `--denied-user-message` answers a mention from a user outside
`--user-ids`, and `--denied-channel-message` answers a mention in a channel
outside `--channel-ids`; each is posted into the mention's thread through the
resident's `POST /messages`. A mention in an unwatched channel is only ever
answered with the channel message. Replies are deduplicated per channel,
thread and user for the life of the subscribe process (a restart may repeat
one), and a failed post is logged and never retried: the bot cannot post into
a channel it has not joined, and that fails the same way every time. Neither
case produces an item.

This is the

`query.subscribe` means `../../config/resources/thread.toml` binds for the
Slack thread resource
(`docs/adr/2026-09-05-standing-session-dispatch.md`), and coexists with
`on_unbound_mention` until that hook's later, separate retirement.

It runs until the connection ends and then exits: staying up is the caller's
job (a supervisor restarting it on any exit, per the ADR's query.subscribe
contract), not this command's. Exit 0 means the caller's own context was
cancelled (e.g. `SIGTERM`); any other exit — the resident adapter was
unreachable, or the stream ended some other way (e.g. an adapter restart) —
is non-zero, and never means "no mentions occurred."

### `resource observe` (thread's observe action)

```
slack-adapter resource observe --resource <permalink>
```

`thread`'s `resource_observer` declaration requires an `observe` action,
but the observer's `state_schema` is empty: a mention's appearance is
already the only fact its `query.subscribe` means reports, and `observe`
has nothing live to add without inventing a fact the schema does not
declare. This command prints `{}` and exits 0 for any non-empty
`--resource`.

## HTTP API

### GET /info

Returns the workspace name and default channel ID. The workspace name is
fetched from Slack's `auth.test` API at startup and cached.

```json
// Response
{"workspace": "my-team", "channel_id": "C..."}
```

### POST /threads

Posts a message to a Slack channel, creating a thread. Returns `thread_ts`,
`channel_id`, and Slack's `chat.getPermalink` URL.

```json
// Request
{"channel_id": "C...", "text": "session start message"}

// Response
{"thread_ts": "1234567890.123456", "channel_id": "C...", "permalink": "https://example.slack.com/archives/C.../p1234567890123456"}
```

### POST /messages

Posts a message to an existing thread. The shipped `slack` channel uses this
endpoint for `plect.judge.recorded` delivery, posting the recorded judge
reason as the reply text.

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "text": "message body"}
```

### POST /subscribe

Registers a `thread_ts` → `socket_path` subscription. slack-adapter keeps
`{thread_ts, channel_id, socket_path, session_name, since, delivered_through}`
in an internal map and routes incoming Slack messages to the right socket in
O(1). Registration also pre-connects to channel-server, so claude's replies
flow to Slack right away. `session_name` is required for app-mention
deliberation delivery because it is the target for `plect event publish`.

**One socket, one thread.** A `socket_path` holds at most one live
registration. A `/subscribe` naming a `socket_path` that already has a
registration under a different `thread_ts` replaces it outright (the
replaced `thread_ts` is logged); the persisted registry applies the same
rule on load, keeping whichever of two same-socket entries has the newer
`since`. This is what keeps a socket from ever holding two thread
destinations at once — otherwise a reply from that session can land in the
wrong thread, or in the channel root, and a bad entry restored from disk on
every restart would misroute silently forever.

`thread_ts` must have the shape Slack actually issues, `<10 digits>.<6
digits>` (e.g. `1234567890.123456`); a `catch_up_through` is checked the
same way. Either field failing the check is a `400`, not a persisted
subscription — a malformed value can never match a real thread, so
persisting it would trade a retriable delivery failure for a permanent
misroute.

An optional `catch_up_through` (a Slack message ts) delivers the thread's
existing history once, on first binding: covers the escalation shape where
people discuss something in a thread and then mention the bot, which
otherwise drops both the triggering mention (no session exists yet to
receive it) and everything said before it. When set, and the subscription's
`delivered_through` is empty or older than it, slack-adapter fetches the
thread, publishes root + every reply through `catch_up_through` as one
inbound `user.emit`, and advances `delivered_through` to it — the same
transcript format app-mention deliberation uses. Re-posting the same
`catch_up_through` (e.g. a runtime restart re-running the run-scoped
`slack_subscribe` task) is a no-op once the watermark already covers it.
Omitting the field leaves behaviour unchanged.

`delivered_through` also survives a clean unsubscribe: `DELETE /subscribe`
tombstones it instead of dropping it, and a subsequent `/subscribe` for the
same `thread_ts` **and** `session_name` restores it before evaluating
`catch_up_through`. This is what keeps a `plect down` / `plect up` cycle (or
an ECS task replacement doing the same) from redelivering the thread
transcript on every deployment — see Persistence below. A different
`session_name` binding the same thread later is a new subscription, not a
resumed one, and gets no watermark.

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "/run/user/1000/claude-channel/<uuid>.sock", "session_name": "owner/repo-1", "catch_up_through": "1234567890.654321"}

// Response
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "...", "session_name": "owner/repo-1", "since": "2026-05-17T00:00:00Z", "delivered_through": "1234567890.654321"}
```

### POST /status

Maps a status-line event to a Slack agent session state without posting a
message. Nonempty `status` means `processing`; empty means `active` after
turn completion. `turn_id` is optional, but enables stale-turn rejection.

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "status": "Checking CI…", "turn_id": "turn-1"}

// Request (clear)
{"thread_ts": "1234567890.123456", "channel_id": "C...", "status": "", "turn_id": "turn-1"}
```

### POST /stream

One chunk of a `plect.message_delta` sequence, or the single chunk a
`plect.message` with no preceding deltas is posted as, rendered as a single
live-updating Slack thread reply via `chat.startStream` /
`chat.appendStream` / `chat.stopStream`. Its identity is the combination of
`channel_id`, `thread_ts`, and `stream_key`; `index` orders chunks within
that identity. A chunk arriving out of order is buffered until the gap
closes or a small bound is reached, at which point the buffered chunks flush
in index order regardless of the gap. `final` finalizes the message. The
`stream` channel that calls this endpoint runs unconditionally, so a
`plect.message` whose own preceding deltas already finalized this identity
reaches it too; a chunk delivered under an already-finalized identity is
dropped rather than starting a second message (`StreamManager`'s own doc
comment, `internal/adapter/stream.go`).

The adapter writes version `1` snapshots to
`$XDG_STATE_HOME/slack-adapter/streams.json` (or
`~/.local/state/slack-adapter/streams.json`). Each atomic snapshot records
in-flight Slack stream timestamps and ordering state, fallback text, and the
bounded finalized-identity set. Startup logs once and starts empty if the
file is missing, unreadable, corrupt, or a different version; that condition
never prevents the adapter from starting.

`chat.startStream` requires a recipient user and that user's workspace
when streaming into a channel. The adapter keeps, per channel and thread, a
queue of the senders of inbound messages that reached their session (thread
messages and app mentions, including a mention handed to an unbound-mention
reader or hook that succeeded); a message whose delivery failed is never
queued. A
stream claims the oldest sender still unanswered when it first appears, so a
message that arrives before a reply's first chunk cannot take that reply
over; it is addressed by a later reply instead. Messages of one turn,
identified by the optional `turn_id` (or else one stream each), share the
turn's sender, and a reply with nobody pending keeps the last sender.
Consecutive messages from the same sender count once. Recipients are never
shared between threads. The workspace is the sender's own for a
shared-channel sender, and the app's otherwise. `allowed_user_ids` only
decides who may talk to a session; it does not choose the recipient, so any
number of allowed users (or none, for app mentions) can stream. The claimed
recipient is kept in the stream snapshot; the queue is in memory, so a
restart between a message and the reply's first chunk can lose it and the
reply falls back as below. Known limit: nothing links a reply to the
inbound message it answers, so a sender who interleaves can shift the
recipient by one turn, and when one agent turn answers several different
senders at once only the oldest is addressed.

If `chat.startStream` itself fails (the workspace/app doesn't support
streaming) or no recipient is known for the thread, every chunk under that
`stream_key` is accumulated instead, and the full text is posted once — via
`POST /messages`'s own mechanics — on `final`. Each such stream logs one
structured line, `stream_start_skipped` (`reason` is `recipient_unknown` or
`recipient_team_unknown`) or `stream_start_failed` (with the Slack error),
carrying the channel, thread and stream key; no line carries a token or
message text. A failure after `chat.startStream` already succeeded (an
`appendStream`/`stopStream` call rejected) is returned to the caller rather
than triggering this fallback: a native message already exists by then, and
posting a second one would violate "exactly one Slack thread message
appears".

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "stream_key": "msg-1", "turn_id": "turn-1", "text": "Hello", "index": "0", "final": "false"}
```

`index` and `final` are strings, not a JSON number/bool: they originate as
event metadata, which is always a string (see `contracts/event`), and the
`stream` channel passes them through verbatim rather than requiring every
composing workflow to convert them first.

### GET /unbound-mentions

Streams one JSON item per line, one per unbound app mention as it occurs
(never a replay of past mentions), while the connection stays open. The
underlying event is the same one `on_unbound_mention` reacts to — both fire
from `app mention skipped: unbound thread` — but this feed is deliberately
unscoped: it does not accept `channel_id` filtering itself, and every
connected reader sees every unbound mention across every channel. Filtering
to specific channels is `subscribe unbound-mentions`'s job (above), so this
endpoint can serve any number of such actions from one Socket Mode
connection. `allowed_user_ids` is applied first, so a mention from a
disallowed user reaches neither this feed nor the hook.

```json
{"resource": "https://<ws>.slack.com/archives/C.../p...", "channel_id": "C...", "thread_ts": "1788222413.916339", "mention_ts": "1788224629.760139", "user_id": "U..."}
```

`resource` is the thread root's permalink, matching `official.slack.thread`'s
`match` regex; `user_id` is the Slack user ID of whoever mentioned the app. A reader that disconnects (its request context ends) is
unregistered; a mention that occurs while nothing is connected is simply
lost, not queued — see `subscribe unbound-mentions` above for the supervised
CLI client built on this feed.

### DELETE /subscribe?thread_ts=... or ?session_name=...

Unsubscribes. An unknown `thread_ts` is a no-op (`204`). Called from `plect
down` / `destroy` cleanup. If the subscription had a non-empty
`delivered_through`, it is preserved as a tombstone (see `POST /subscribe`
above and Persistence below), not discarded.

`?session_name=...` drops every registration for that session regardless of
`thread_ts` — the operator escape hatch for a stale or malformed entry whose
exact `thread_ts` string isn't known (an unknown `session_name` is likewise
a no-op). `?thread_ts=...` still exists for the single-registration case;
the two query parameters are mutually exclusive forms of the same request,
not composable.

### GET /subscribers

Returns the current subscription list. Used by the task's `[health].alive`
probe to determine whether its own `thread_ts` is still registered with the
broker. If a broker restart dropped it, the probe goes non-zero and the next
`plect up` re-runs setup.

```json
{"subscribers": [{"thread_ts": "...", "channel_id": "...", "socket_path": "...", "since": "..."}]}
```

## Routing

`thread_ts` → `socket_path` resolves through an in-memory map. Subscriptions
are registered explicitly via `/subscribe`.

On an incoming message, if the subscriber's `socket_path` no longer exists, it
is lazily removed from the map and a failure notice is posted to the Slack
thread.

## Persistence

Subscriptions and unsubscribed threads' delivery tombstones are persisted to
`$XDG_STATE_HOME/slack-adapter/subscribers.json` (default
`~/.local/state/slack-adapter/subscribers.json`) as
`{"subscribers": [...], "tombstones": [...]}`, with an atomic write (tmp →
rename) on every subscribe/unsubscribe. Restarting slack-adapter reads it
back at startup, so subscriptions survive a restart and routing continues
without any action on the plect side. A failed read-back (missing or corrupt
file, including the bare-array shape this file held before tombstones
existed — see `docs/migrations/slack-adapter-subscribers-envelope-migration.md`)
logs a warning and starts with an empty state, which the next `/subscribe`
repopulates.

**Operational requirement:** this state directory must live on the same
persistent storage as plect's own state. `official.slack.slack_subscribe` is
run-scoped — its cleanup runs `DELETE /subscribe` on every `plect down`, and
the following `plect up` re-subscribes — so the tombstoned `delivered_through`
watermark is the only thing standing between a routine restart and
redelivering the whole thread transcript to the resumed session. In a
container, put `HOME` (or `XDG_STATE_HOME` directly) on the volume that
survives a redeploy. A tombstone older than 30 days is dropped on load and on
every persist, regardless of storage — it is assumed no session will resume
against it by then.

## Setting up the Slack App

For outbound threads only:

1. Create an app at [Slack API](https://api.slack.com/apps)
2. Add Bot Token Scopes under **OAuth & Permissions**:
   - `chat:write` -- post messages
3. Install to the workspace and get the Bot Token (`xoxb-`)
4. Invite the bot to the target channel (`/invite @botname`)

See `slack-app-manifest.yml` for the app manifest.

For inbound thread deliberation, enable Socket Mode, create an app-level token
with `connections:write`, configure it as `SLACK_APP_TOKEN`, subscribe the app
to `app_mention`, and add Bot Token Scopes:

- `app_mentions:read` -- receive bot mentions
- `channels:history` -- fetch public-channel thread replies
- `groups:history` -- fetch private-channel thread replies, only when private
  channels are used
- `users:read` -- resolve display names

## Claude Code hooks (`~/.claude/settings.json`)

The SessionStart / Stop / TaskCreated / SubagentStart / SubagentStop hooks
post status updates to the Slack thread. Add the following to `settings.json`
(replace the command path with wherever you install the notify script, e.g.
`/path/to/your/scripts/claude-slack-notify.sh`):

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/scripts/claude-slack-notify.sh"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/scripts/claude-slack-notify.sh"
          }
        ]
      }
    ],
    "TaskCreated": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/scripts/claude-slack-notify.sh"
          }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/scripts/claude-slack-notify.sh"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/your/scripts/claude-slack-notify.sh"
          }
        ]
      }
    ]
  }
}
```

The hook script no-ops for sessions without `SLACK_THREAD_TS` set (i.e.
non-plect sessions).

## Running as a service

slack-adapter is a long-running broker process; run it under whatever process
supervisor your environment uses (systemd user service, launchd, a process
manager, etc.), started as `slack-adapter` and left resident. channel-server
is spawned per-session by Claude Code itself and doesn't need its own service
entry.
