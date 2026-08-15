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

## Install

Requires [`agent/runtime`](../runtime) enabled from the same catalog — the
activity hooks resolve `plect-agent-activity` through it — and, optionally,
a `channel-server` build on `PATH` for the socket-delivered channel above
(see `plugins/channel-server` in this repository).

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --dir plugins --revision <tag-or-commit>
plect plugin add official/agent/runtime
plect plugin add official/agent/claude
```

## Inputs

`tmux_session` (required, typically wired from `agent/runtime`'s `tmux`
task), `model`, `effort` — see `tasks/claude.toml` for the exact flag each
one appends.

## Not included

- Any workflow composing this task with a resource provider (e.g. GitHub) —
  workflows compose across plugins and their packaging is decided when that
  provider plugin lands.
- Chat/notification channel bindings (Slack, or any other tool), MCP servers
  beyond channel-server, and CLI write-guards (e.g. a shim that denies `gh pr
  merge`) — these are operator/team choices, not shipped defaults. Add them
  by placing a same-id `claude` task definition in your own trusted overlay
  (whole-definition replacement; see this repository's plugin-packaging
  design doc for the shadowing model).
