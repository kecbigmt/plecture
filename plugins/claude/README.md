# claude

Claude Code launch task, initial-prompt submit/readiness composition, and
structured delivery via the channel-server daemon. Split out of the former
`session/runtime` plugin per `docs/design/plugin-boundary-contracts.md`;
`gh-guard` moved to the `github` plugin, and `tmux` is a separate,
independently selectable plugin this one composes through `{{terminal
"..."}}` — never a direct dependency.

## Contents

- `config/tasks/claude.toml` — the `claude` task. `setup` launches `claude`
  (fresh or `--resume`, with a same-session-id fallback if resume finds no
  persisted conversation) via `{{terminal "send_text"}}`/`{{terminal
  "send_keys"}}`, waits for it to come up by polling
  `~/.claude/sessions/*.json`, wires a channel-server MCP socket when
  `channel-server` is on `PATH`, and registers turn-boundary activity
  hooks. `[health].alive` self-heals a stale pid by re-deriving the live
  process from the pane's process tree (still a direct, documented tmux
  dependency — no `[terminal]` verb covers process discovery), so a
  crash-and-relaunch or a manual `--resume` does not require a session
  down/up to keep event delivery working. `[health].activity` reads back the
  record those turn-boundary hooks write, so hook and probe are two halves of
  one fingerprint format.
- `config/tasks/claude_initial_prompt.toml` — sends a session's initial prompt via
  `{{terminal "..."}}` once the CLI's input box is visible, or on every
  `plect up` when `repeat = "true"`.
- `config/channels/claude.toml` — delivers a session event to the running
  Claude Code process over its channel-server Unix socket.
- `scripts/claude-agent-activity` — both halves of the turn-boundary activity
  fingerprint: the hook the `claude` task registers, and the `probe` verb that
  task declares as its `[health].activity`.
- `src/channel-server/` — generic message delivery to Claude Code, with no
  knowledge of message sources (Slack or otherwise). See
  `src/channel-server/CLAUDE.md`.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/claude
```

Building `channel-server` requires a Go toolchain (see the Package format
section of `docs/design/plugin-packaging.md`); `plect plugin add`/`update`
builds it automatically.

A session using this plugin also needs a `[terminal]`-declaring task in the
workflow (e.g. `official/tmux`'s `tmux` task) — this plugin's own tasks
never declare `[terminal]` themselves.

## Resident-supervised Services

`channel-server` is declared as a `[[services]]` in `plugin.toml`,
supervised by `plect serve` (start, crash-restart with backoff, stop with
the resident process). Its declaration stays inert today by design: real
instances are per-session, launched by Claude Code itself via MCP
configuration with a session-specific `CHANNEL_SOCKET_PATH` the resident
process never has — see `plugin.toml`'s comment and
`docs/adr/2026-08-16-plugin-service-lifecycle.md`'s Consequences section.

## Not included

- Which agent CLI a session launches beyond Claude Code, and any model/
  effort defaults for other CLIs — a workflow's concern.
- The write guard for `gh` — see the `github` plugin's `gh_guard` task; this
  plugin's `claude` task accepts only a generic `path_prepend` input, never
  a GitHub-specific switch.
- A no-channel-server interactive Claude configuration is outside the
  supported surface — see `docs/migrations/` for the migration procedure.
