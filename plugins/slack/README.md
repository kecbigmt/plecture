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

- `config/tasks/slack_thread.toml` — creates one Slack root message for a PR
  review session through `slack-adapter` and records the conversation with
  `plect state set-conversation`. Outputs: `thread_ts`, `channel_id`, and
  `permalink`.
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

## Review thread contract

`slack_thread` creates the root message as:

```text
[AI review] <PR title> — <PR URL>
session <name> · head <short sha>
```

It records the session conversation with `source=Slack`, the adapter
permalink, and metadata keys `thread_ts`, `channel_id`, `pr_url`, and
`review_session`.

The final review conclusion is an event with type `plect.review.conclusion`.
The Slack channel should be bound with `include = ["plect.review.conclusion"]`;
the posted text is the event body, falling back to the summary. Progress,
heartbeats, terminal events, and GitHub watcher events do not match that
binding.

See `examples/review-thread-workflow.toml` for a user-owned composition that
wires `slack_thread` outputs into the `slack` channel.

## Not included

- Which agent runtime delivers into Slack, or vice versa — a workflow's
  concern, composed by wiring this plugin's channel and any agent-runtime
  plugin's own tasks/channels together. This plugin never connects to a
  channel-server socket or imports an agent-runtime plugin's package.
- slack-adapter's Slack App manifest, HTTP API surface, and subscription
  broker behavior — see `src/slack-adapter/CLAUDE.md`.
