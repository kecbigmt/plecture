# session/runtime

The session runtime surface for Claude Code and Codex CLI: shared tmux pane
lifecycle, per-agent launch tasks, the channel-server/slack-adapter delivery
daemons, and the Slack delivery channel that talks to them. One plugin
because these pieces share runtime contracts that a plugin boundary must not
cut through — see `docs/adr/2026-08-16-plugin-service-lifecycle.md`'s
Plugin Boundary Rule.

## Contents

- `config/tasks/tmux.toml` — creates/attaches/tears down the tmux pane an
  agent CLI runs in. `attach`/`capture` let `plect attach`/`plect show
  --capture` reach the pane; `healthcheck` is a plain `tmux has-session`.
- `config/tasks/initial_prompt.toml` — sends a session's initial prompt into
  the pane once the CLI's input box is visible, or on every `plect up` when
  `repeat = "true"`.
- `config/channels/tmux_send_keys.toml` — an event channel that types a
  later event into the same pane (for a CLI runtime with no structured
  delivery transport).
- `config/tasks/claude.toml` — the `claude` task. `setup` launches `claude`
  in a tmux pane (fresh or `--resume`, with a same-session-id fallback if
  resume finds no persisted conversation), waits for it to come up by
  polling `~/.claude/sessions/*.json`, wires a channel-server MCP socket
  when `channel-server` is on `PATH`, and registers turn-boundary activity
  hooks. `healthcheck` self-heals a stale pid by re-deriving the live
  process from the pane's process tree, so a crash-and-relaunch or a manual
  `--resume` does not require a session down/up to keep event delivery
  working.
- `config/channels/claude.toml` — delivers a session event to the running
  Claude Code process over its channel-server Unix socket.
- `config/tasks/codex.toml` — the interactive Codex TUI task. Codex has no
  Claude Code-style structured transport, so a running interactive session
  only accepts input by having text typed into its tmux pane (via
  `tmux_send_keys`/`initial_prompt`).
- `config/tasks/codex_exec.toml` + `config/channels/codex_exec.toml` — the
  headless exec shape: starts `plect-codex-exec-worker` in the pane instead,
  which drains a per-session queue directory serially into `codex
  exec`/`codex exec resume`. A later event is delivered by appending to that
  queue (`plect-codex-exec-enqueue`) rather than by typing into a pane, so
  the submit-race and boot-race classes the interactive shape exists to
  solve do not apply here — there is no input box to wedge.
- `config/channels/slack.toml` — delivers a session event to its Slack
  thread by posting to the `slack-adapter` service's HTTP API. Inputs:
  `base_url` (e.g. `http://127.0.0.1:7890` for its default `listen_addr`),
  `channel_id`, and `thread_ts` (typically wired from whatever task created
  the thread via slack-adapter's `POST /threads`).
- `scripts/plect-claude-agent-activity`, `scripts/plect-codex-agent-activity`
  — the turn-boundary self-report hooks each agent's task registers.
  Branch-free per-agent scripts rather than one shared script parameterized
  by agent name — see the ADR referenced above.
- `scripts/plect-codex-exec-worker` — the worker script `codex_exec.toml`
  launches, resolved through `{{bin ...}}` so it needs no `PATH` entry of
  its own.
- `scripts/plect-codex-exec-enqueue` — the enqueue script `channels/
  codex_exec.toml` runs. **A channel's `command` is never
  template-rendered** (only its `args` are, and only with the `json`
  function — `{{bin ...}}` exists only for task setup/cleanup hooks), so
  this script must be reachable on `PATH` under its own name for the
  channel to work.
- `src/channel-server/` — generic message delivery to Claude Code, with no
  knowledge of message sources (Slack or otherwise). See
  `src/channel-server/CLAUDE.md`.
- `src/slack-adapter/` — Slack-specific message relay + subscription
  broker; channel-server's client. See `src/slack-adapter/CLAUDE.md`.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/session/runtime
```

Building `channel-server` and `slack-adapter` requires a Go toolchain (see
the Package format section of `docs/design/plugin-packaging.md`); `plect
plugin add`/`update` builds them automatically.

If a workflow uses `config/channels/codex_exec.toml`, put this plugin's
`scripts/` on `PATH` (or symlink `plect-codex-exec-enqueue` onto it) — see
the note on channel `command` above.

## Bus-supervised services

`channel-server` and `slack-adapter` are declared as `[[services]]` in
`plugin.toml`, supervised by `plect bus serve` (start, crash-restart with
backoff, stop with the bus). `slack-adapter` needs `SLACK_BOT_TOKEN` and
`SLACK_APP_TOKEN` in the bus process's own environment before it starts —
without them it stays inert rather than crash-looping. `channel-server`'s
service declaration stays inert today by design: real instances are
per-session, launched by Claude Code itself via MCP configuration with a
session-specific `CHANNEL_SOCKET_PATH` the bus process never has — see
`plugin.toml`'s comment and the ADR's Consequences section.

## Not included

- Which agent CLI a session launches, and its model/effort defaults, are a
  workflow's concern, composed by referencing this plugin's task ids.
- Any workflow composing these tasks/channels with a resource provider
  (e.g. GitHub) — workflows compose across plugins and stay out of any
  single plugin.
- Chat/notification channel bindings beyond Slack, MCP servers beyond
  channel-server, and CLI write-guards (e.g. a shim that denies `gh pr
  merge`) — operator/team choices, not shipped defaults. Add them by
  placing a same-id task/channel definition in your own trusted overlay
  (whole-definition replacement; see `docs/design/plugin-packaging.md`'s
  shadowing model).
- slack-adapter's Slack App manifest, HTTP API surface, and subscription
  broker behavior — see `src/slack-adapter/CLAUDE.md`.
