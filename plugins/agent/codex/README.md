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

## Install

Requires [`agent/runtime`](../runtime) enabled from the same catalog — both
tasks' activity hooks resolve `plect-agent-activity` through it.

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --dir plugins --revision <tag-or-commit>
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
`agent/runtime`'s `tmux` task), `model`, and `effort` — see each task file
for the exact flag each one appends.

## Not included

- Any workflow composing these tasks/channels with a resource provider
  (e.g. GitHub) — workflows compose across plugins and their packaging is
  decided when that provider plugin lands.
- Chat/notification channel bindings and CLI write-guards — operator/team
  choices, not shipped defaults. Add them by placing a same-id task
  definition in your own trusted overlay (whole-definition replacement; see
  this repository's plugin-packaging design doc for the shadowing model).
