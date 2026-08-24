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
            │ in-memory map: thread_ts → {channel_id, socket_path, since}
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
```

Outbound-only operation requires only `slack_bot_token`. Requests that omit
`channel_id` require the optional configured default.

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
`{thread_ts, channel_id, socket_path, since}` in an internal map and routes
incoming Slack messages to the right socket in O(1). Registration also
pre-connects to channel-server, so claude's replies flow to Slack right away.

```json
// Request
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "/run/user/1000/claude-channel/<uuid>.sock"}

// Response
{"thread_ts": "1234567890.123456", "channel_id": "C...", "socket_path": "...", "since": "2026-05-17T00:00:00Z"}
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

For outbound review threads only:

1. Create an app at [Slack API](https://api.slack.com/apps)
2. Add Bot Token Scopes under **OAuth & Permissions**:
   - `chat:write` -- post messages
3. Install to the workspace and get the Bot Token (`xoxb-`)
4. Invite the bot to the target channel (`/invite @botname`)

See `slack-app-manifest.yml` for the app manifest.

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
