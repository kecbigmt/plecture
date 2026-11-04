# slack-adapter — Developer Guide

## Responsibility

The adapter receives Slack Socket Mode events, stores thread subscriptions, publishes inbound events to plect session event logs, and posts outbound messages through Slack's HTTP API. It does not connect to an agent runtime socket.

The ledger is `$XDG_STATE_HOME/slack-adapter/subscribers.json` (default `~/.local/state/slack-adapter/subscribers.json`). It records thread subscriptions by `(session_name, thread_ts)` and mention subscriptions by `(session_name, "mentions")`. Mention delivery watermarks are stored separately in the same ledger by `(session_name, thread_ts)` and are deleted when the mention subscription is removed. A startup sweep calls `plect ls --json` and removes subscriptions for destroyed sessions, retaining entries for down sessions. Each successful `user.emit` publication advances that subscriber's watermark; `slack.message` does not.

## Boundaries

- Do not import `plect/app` or another plugin module. Invoke the installed `plect` CLI to publish events and list sessions.
- The thread and mention resources' `subscribe` and `unsubscribe` actions use this binary's `resource` CLI and the resident adapter's `/subscribe` endpoint.
- `resource observe` prints `{}` because the observer has no live state beyond the query stream.
- `subscribe unbound-mentions` consumes the resident adapter's `/unbound-mentions` feed. It does not open a second Socket Mode connection.

## HTTP API

- `GET /info`: workspace and default channel.
- `POST /threads`: create a root message and return its permalink.
- `POST /messages`, `POST /status`, `POST /stream`: outbound delivery.
- `POST /subscribe`: register a thread with `{session_name, thread_ts, channel_id, catch_up_through?}` or a mention subscription with `{session_name, thread_ts:"mentions", team_id}`.
- `DELETE /subscribe?session_name=...&thread_ts=...`: remove one pair. `DELETE /subscribe?session_name=...` removes all pairs for a session.
- `GET /subscribers`: list active subscriptions.
- `GET /unbound-mentions`: stream mentions without a bound thread.
- `POST /notify`: legacy Slack notification endpoint; it has no runtime socket delivery.

## Testing

Run `go test ./...` from this module. The Slack API is replaced with local test servers in tests.
