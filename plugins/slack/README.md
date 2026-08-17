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
the resident process). It needs `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, and
`SLACK_CHANNEL_ID` in the resident process's own environment before it
starts — without all three it stays inert rather than crash-looping (its
`cmd/slack-adapter/main.go` exits at startup if any is unset).

## Not included

- Which agent runtime delivers into Slack, or vice versa — a workflow's
  concern, composed by wiring this plugin's channel and any agent-runtime
  plugin's own tasks/channels together. This plugin never connects to a
  channel-server socket or imports an agent-runtime plugin's package.
- slack-adapter's Slack App manifest, HTTP API surface, and subscription
  broker behavior — see `src/slack-adapter/CLAUDE.md`.
