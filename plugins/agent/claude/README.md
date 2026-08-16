# agent/claude

Launches and manages a Claude Code runtime as the primary task of a session.

## Contents

- `tasks/claude.toml` — the `claude` task. `setup` launches `claude` in a
  tmux pane (fresh or `--resume`, with a same-session-id fallback if resume
  finds no persisted conversation), waits for it to come up by polling
  `~/.claude/sessions/*.json`, wires a channel-server MCP socket when
  `channel-server` is on `PATH`, and registers turn-boundary activity hooks.
  `cleanup` terminates the process and removes its temp files. `healthcheck`
  self-heals a stale pid by re-deriving the live process from the pane's
  process tree, so a crash-and-relaunch or a manual `--resume` does not
  require a session down/up to keep event delivery working.
- `channels/claude.toml` — delivers a session event to the running Claude
  Code process over its channel-server Unix socket.
- `bin/plect-agent-activity` — the turn-boundary self-report hook
  `tasks/claude.toml`'s setup registers. A copy of `agent/runtime`'s script
  of the same name — see that plugin's README for why it's a copy rather
  than a cross-plugin reference.
- `bin/gh-guard` — a `gh` shim that mechanically denies `pr merge`/`issue
  close`/`pr close` (and their `gh api` equivalents), so a session can't act
  on a forgotten or de-prioritized "don't merge" instruction. `tasks/claude.toml`'s
  setup symlinks this onto the session's `PATH` as `gh` only when the task's
  `gh_guard` input is set — unset (the default) launches with no shim. A copy
  of `agent/codex`'s script of the same name, for the same cross-plugin
  reason as `plect-agent-activity` above.

## Install

Typically composed with [`agent/runtime`](../runtime) enabled from the same
catalog — its `tmux` task is the usual source of the `tmux_session` input
this plugin's task requires — but that pairing is a workflow-level choice,
not a hard dependency: `plect-agent-activity` is this plugin's own `bin/`.
Optionally, a `channel-server` build on `PATH` enables the socket-delivered
channel above (see `plugins/channel-server` in this repository).

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/agent/runtime
plect plugin add official/agent/claude
```

## Inputs

`tmux_session` (required, typically wired from `agent/runtime`'s `tmux`
task), `model`, `effort`, `gh_guard` — see `tasks/claude.toml` for the exact
flag/effect each one appends. `gh_guard` is opt-in and defaults to unset (no
shim, no behavior change from before this input existed).

## Not included

- Any workflow composing this task with a resource provider (e.g. GitHub) —
  workflows compose across plugins and their packaging is decided when that
  provider plugin lands.
- Chat/notification channel bindings (Slack, or any other tool) and MCP
  servers beyond channel-server — these are operator/team choices, not
  shipped defaults. Add them by placing a same-id `claude` task definition in
  your own trusted overlay (whole-definition replacement; see this
  repository's plugin-packaging design doc for the shadowing model).
