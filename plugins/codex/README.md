# codex

Codex TUI and `codex exec` launch tasks, initial-prompt/terminal-submit
readiness composition, and the headless exec worker/enqueue pair. Split
out of the former `session/runtime` plugin per
`docs/design/plugin-boundary-contracts.md`; `gh-guard` moved to the
`github` plugin, and `tmux` is a separate, independently selectable plugin
this one composes through `{{terminal "..."}}` — never a direct
dependency.

## Contents

- `config/tasks/codex.toml` — the interactive Codex TUI task. Launch
  keystrokes go through `{{terminal "send_text"}}`/`{{terminal
  "send_keys"}}`; process-tree pid discovery and TUI-readiness polling
  (`tmux display-message`/`tmux capture-pane`, via `{{terminal
  "capture"}}` where possible) remain direct, documented tmux dependencies
  — no `[terminal]` verb covers "what pid does this endpoint run as" or
  raw pane introspection beyond a capture.
- `config/tasks/codex_initial_prompt.toml` — sends a session's initial prompt via
  `{{terminal "..."}}` once the CLI's input box is visible, or on every
  `plect up` when `repeat = "true"`.
- `config/channels/terminal_submit.toml` — an event channel that types a
  later event into the session's terminal via `{{terminal "..."}}`, for a
  runtime with no structured delivery transport. This plugin owns the
  burst-split/retry/readiness composition (see
  `docs/adr/2026-08-17-plugin-boundary-contracts.md`'s Codex Terminal
  Submit) because it describes the Codex TUI contract, not the
  multiplexer's.
- `config/tasks/codex_exec.toml` + `config/channels/codex_exec.toml` — the
  headless exec shape: starts `codex-exec-worker` via `{{terminal
  "..."}}` instead of the interactive TUI, which drains a per-session queue
  directory serially into `codex exec`/`codex exec resume`. A later event
  is delivered by appending to that queue (`codex-exec-enqueue`) rather
  than by typing into a pane, so the submit-race and boot-race classes the
  interactive shape exists to solve do not apply here — there is no input
  box to wedge.
- `scripts/codex-agent-activity` — both halves of the turn-boundary activity
  fingerprint: the hook the `codex`/`codex_exec` tasks register, and the
  `probe` verb those tasks declare as their `[health].activity`.
- `scripts/codex-exec-worker` — the worker script `codex_exec.toml`
  launches, resolved through `{{bin ...}}` so it needs no `PATH` entry of
  its own.
- `scripts/codex-exec-enqueue` — the enqueue script `channels/
  codex_exec.toml` runs. **A channel's `command` is never
  template-rendered** (only its `args` are — `{{bin ...}}` exists only for
  task setup/cleanup hooks and channel args via `{{terminal ...}}`), so
  this script must be reachable on `PATH` under its own name for the
  channel to work.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/codex
```

If a workflow uses `config/channels/codex_exec.toml`, put this plugin's
`scripts/` on `PATH` (or symlink `codex-exec-enqueue` onto it) — see the
note on channel `command` above.

A session using this plugin also needs a `[terminal]`-declaring task in the
workflow (e.g. `official/tmux`'s `tmux` task) — this plugin's own tasks
never declare `[terminal]` themselves.

## Not included

- The write guard for `gh` — see the `github` plugin's `gh_guard` task;
  this plugin's tasks accept only a generic `path_prepend` input, never a
  GitHub-specific switch.
