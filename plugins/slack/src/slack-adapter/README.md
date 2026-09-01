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
status_loading_messages = ["Checking…"] # optional; shown on a thread's shimmer status line, max 10 entries
status_ttl = "15m"                      # optional; clears a stale status if nothing posts by then
```

Outbound-only operation requires only `slack_bot_token`. Requests that omit
`channel_id` require the optional configured default.

### Thread status (shimmer)

When Socket Mode delivers an inbound message or app-mention deliberation to
a session, slack-adapter shows the bound thread's assistant status line
(`assistant.threads.setStatus`) with `status_loading_messages` (empty shows
Slack's own default text). The status is cleared once the session posts a
reply or a permission prompt through this adapter, or after `status_ttl`
elapses with nothing posted (covers a session that ends its turn without
ever calling reply). `status_loading_messages` is plain config — this plugin
has no built-in wording tied to any particular workflow.

There is no `status_text` config: confirmed empirically against real Slack
workspaces, a channel thread never renders `assistant.threads.setStatus`'s
`status` string, only `loading_messages`. `SetThreadStatus`'s `status`
parameter (and `POST /status`'s `status` field) stay purely an on/off flag
because of this — the API still requires a non-empty value to mean "show" —
and a caller who wants text supplies it via `loading_messages`.
`StatusManager.Set` always clears before it sets, because a
`loading_messages` entry sent right after a prior status-only call on the
same thread was observed to flash once and revert to Slack's default text,
while the same entry sent right after an explicit clear renders
persistently.

When Socket Mode is enabled, an `app_mention` in a subscribed thread publishes
one inbound `user.emit` to the bound `session_name`. The event body is an
ordered Slack transcript with display names, message times, and text. By
default each delivery includes the root message plus only replies after the
thread's last successful delivery watermark, followed by the mentioning
message. Set `deliver_full_thread = true` to send the full thread on every
mention. Empty `allowed_user_ids` allows any channel member to drive this
mention path; setting it restricts app mentions to the listed Slack users.

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

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "/run/user/1000/claude-channel/<uuid>.sock", "session_name": "owner/repo-1"}

// Response
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "...", "session_name": "owner/repo-1", "since": "2026-05-17T00:00:00Z"}
```

### POST /status

Sets or clears a thread's assistant shimmer status line directly, without
posting a message. `status` is only an on/off flag (empty clears; any
non-empty value shows); the visible text belongs in `loading_messages` (max
10) — `status`'s own content never renders in a channel thread. Wiring this
to agent hooks (e.g. reporting the current tool name as status) is left to a
caller of this endpoint, not built into the plugin.

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "status": "1", "loading_messages": ["Checking CI…"]}

// Request (clear)
{"thread_ts": "1234567890.123456", "channel_id": "C...", "status": ""}
```

### DELETE /subscribe?thread_ts=...

Unsubscribes. A missing `thread_ts` is a no-op (`204`). Called from `plect
down` / `destroy` cleanup.

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

The map is persisted to `$XDG_STATE_HOME/slack-adapter/subscribers.json`
(default `~/.local/state/slack-adapter/subscribers.json`) with an atomic write
(tmp → rename) on every subscribe/unsubscribe. Restarting slack-adapter reads
it back at startup, so subscriptions survive a restart and routing continues
without any action on the plect side. A failed read-back (missing or corrupt
file) logs a warning and starts with an empty map, which the next `/subscribe`
repopulates.

On an incoming message, if the subscriber's `socket_path` no longer exists, it
is lazily removed from the map and a failure notice is posted to the Slack
thread.

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
