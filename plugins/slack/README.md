# slack

The Slack adapter service, thread binding, and the Slack delivery channel.
Split out of the former `session/runtime` plugin per
`docs/design/plugin-boundary-contracts.md`. Excludes agent runtimes and
channel-server sockets: `slack-adapter` connects to a Unix socket only in
its own test doubles (a protocol-conformant fake it owns —
`src/slack-adapter/internal/adapter/fakesocket_test.go` — built on
`contracts/channel-protocol`), never in production code or by importing
another plugin's package.

## Contents

- `config/tasks/slack_thread.toml` — creates one Slack root message through
  `slack-adapter` and records the conversation with
  `plect state set-conversation`. Outputs: `thread_ts`, `channel_id`, and
  `permalink`.
- `config/tasks/slack_subscribe.toml` — run-scoped effect that registers a
  thread ↔ runtime binding with `slack-adapter` (`POST /subscribe`, undone
  with `DELETE /subscribe` on cleanup), so an `app_mention` in the thread
  reaches the session over Socket Mode. Inputs: `base_url`, `thread_ts`,
  `channel_id`, and `socket_path` (an opaque string — wire it from whatever
  runtime task exposes its channel-server socket). Run scope, not session
  scope, because the socket path changes on every agent launch while the
  thread does not.
- `config/channels/slack.toml` — delivers a session event to its Slack
  thread by posting to the `slack-adapter` service's HTTP API. Inputs:
  `base_url` (e.g. `http://127.0.0.1:7890` for its default `listen_addr`),
  `channel_id`, and `thread_ts` (typically wired from whatever task created
  the thread via slack-adapter's `POST /threads`).
- `src/slack-adapter/` — Slack-specific message relay + subscription
  broker. See `src/slack-adapter/CLAUDE.md`.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/slack
```

Building `slack-adapter` requires a Go toolchain (see the Package format
section of `docs/design/plugin-packaging.md`); `plect plugin add`/`update`
builds it automatically.

## Resident-supervised Services

`slack-adapter` is declared as a `[[services]]` in `plugin.toml`,
supervised by `plect serve` (start, crash-restart with backoff, stop with
the resident process). Outbound thread creation and message posting require
only `SLACK_BOT_TOKEN`; `SLACK_APP_TOKEN` is optional and only activates
Socket Mode inbound relay.

## Thread contract

`slack_thread` posts whatever `root_text` input it is handed as the root
message and records the session conversation with `source=Slack`, the
adapter permalink, and metadata keys `thread_ts` and `channel_id`. It carries
no opinion about what the thread is for — review, or anything else — and no
PR-specific inputs: how the root text is framed is the composing workflow's
concern.

`examples/review-thread-workflow.toml` shows the review case: it composes
`root_text` from a PR title, URL, and head sha, then wires `slack_thread`
outputs into the `slack` channel bound with `include =
["plect.judge.recorded"]`. Its conclusion event is the `plect.judge.recorded`
event that plect appends when a reviewer records a done_when judge verdict;
the body is the recorded judge reason, so the Slack reply text is exactly the
conclusion the reviewer recorded, and metadata carries `instance`, `leaf_id`,
`action`, `revision`, `reviewer_session`, `reviewer_workflow`, and
`relation`. The posted text is the event body, falling back to the summary
only for legacy events without a body; progress, heartbeats, terminal events,
and GitHub watcher events do not match that binding.

The same example also shows the binding for the `status` channel. Its
workflow expression maps `plect.status_message` events with summary `waiting`
to an empty `status` input, which clears the Slack thread status line, and
passes every other summary through as the visible text. The binding supplies
the documented `status` channel inputs: `base_url`, `channel_id`, `thread_ts`,
and `status`.

## Presentation-only exceptions

`slack-adapter`'s deprecated `/notify` rollback path (see
`src/slack-adapter/CLAUDE.md`) inspects a GitHub-derived `change_type` to
pick an emoji prefix, falling back to a generic prefix for anything else.
This is a display detail of an already-deprecated path, not a workflow
commitment the config layer makes, so it is left as is rather than
generalized.

## Not included

- Which agent runtime delivers into Slack, or vice versa — a workflow's
  concern, composed by wiring this plugin's channel and any agent-runtime
  plugin's own tasks/channels together. This plugin never connects to a
  channel-server socket or imports an agent-runtime plugin's package.
- slack-adapter's Slack App manifest, HTTP API surface, and subscription
  broker behavior — see `src/slack-adapter/CLAUDE.md`.
