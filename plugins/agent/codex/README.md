# agent/codex

Launches and manages a Codex CLI runtime, in either of two shapes:

- **Interactive TUI** (`tasks/codex.toml`) — Codex has no Claude
  Code-style structured transport, so a running interactive session only
  accepts input by having text typed into its tmux pane (via
  `agent/runtime`'s `tmux_send_keys` channel and `initial_prompt` task).
- **Headless exec worker** (`tasks/codex_exec.toml` +
  `channels/codex_exec.toml`) — starts `plect-codex-exec-worker` in the pane
  instead, which drains a per-session queue directory serially into `codex
  exec`/`codex exec resume`. A later event is delivered by appending to that
  queue (`plect-codex-exec-enqueue`) rather than by typing into a pane, so
  the submit-race and boot-race classes `agent/runtime` exists to solve do
  not apply to this shape — there is no input box to wedge.

Pick whichever task/channel pair a workflow needs; both are shipped so a
workflow can compose either without hand-authoring it.

## Contents

- `tasks/codex.toml` — the interactive `codex` task.
- `tasks/codex_exec.toml` — the headless `codex_exec` task.
- `channels/codex_exec.toml` — the exec channel that enqueues an event for
  the headless worker.
- `bin/plect-codex-exec-worker` — the worker script `codex_exec.toml`
  launches. Resolved by `codex_exec.toml`'s setup through `{{bin ...}}`, so
  it needs no `PATH` entry of its own.
- `bin/plect-codex-exec-enqueue` — the enqueue script the exec channel
  above runs. **A channel's `command` is never template-rendered** (only its
  `args` are, and only with the `json` function — `{{bin ...}}` exists only
  for task setup/cleanup hooks), so this script must be reachable on `PATH`
  under its own name for the channel to work.
- `bin/plect-agent-activity` — the turn-boundary self-report hook both tasks'
  setup registers. A copy of `agent/runtime`'s script of the same name — see
  that plugin's README for why it's a copy rather than a cross-plugin
  reference.
- `bin/gh-guard` — a `gh` shim that mechanically denies `pr merge`/`issue
  close`/`pr close` (and their `gh api` equivalents), so a session can't act
  on a forgotten or de-prioritized "don't merge" instruction. Both tasks'
  setup symlinks this onto the session's `PATH` as `gh` only when the task's
  `gh_guard` input is set — unset (the default) launches with no shim. A copy
  of `agent/claude`'s script of the same name, for the same cross-plugin
  reason as `plect-agent-activity` above.

## Install

Typically composed with [`agent/runtime`](../runtime) enabled from the same
catalog — its `tmux` task is the usual source of the `tmux_session` input
both of this plugin's tasks require — but that pairing is a workflow-level
choice, not a hard dependency: `plect-agent-activity` is this plugin's own
`bin/`.

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/agent/runtime
plect plugin add official/agent/codex
```

If a workflow uses `channels/codex_exec.toml`, put this plugin's `bin/` on
`PATH` (or symlink `plect-codex-exec-enqueue` onto it):

```bash
export PATH="/path/to/plugin/cache/agent/codex/bin:$PATH"
```

## Inputs

Both tasks take `tmux_session` (required, typically wired from
`agent/runtime`'s `tmux` task), `model`, `effort`, and `gh_guard` — see each
task file for the exact flag/effect each one appends. `gh_guard` is opt-in
and defaults to unset (no shim, no behavior change from before this input
existed).

## Not included

- Any workflow composing these tasks/channels with a resource provider
  (e.g. GitHub) — workflows compose across plugins and their packaging is
  decided when that provider plugin lands.
- Chat/notification channel bindings — operator/team choices, not shipped
  defaults. Add them by placing a same-id task definition in your own
  trusted overlay (whole-definition replacement; see this repository's
  plugin-packaging design doc for the shadowing model).
