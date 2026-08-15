# agent/runtime

Shared pieces used by both the `agent/claude` and `agent/codex` plugins: a
tmux pane lifecycle task, provider-agnostic initial-prompt delivery, a
tmux-keystroke event channel, and a turn-boundary activity self-report hook.
None of this is CLI-specific — both plugins depend on it, which is why it is
its own plugin instead of being duplicated in each.

## Contents

- `tasks/tmux.toml` — creates/attaches/tears down the tmux pane an agent CLI
  runs in. `attach`/`capture` let `plect attach`/`plect show --capture` reach
  the pane; `healthcheck` is a plain `tmux has-session`.
- `tasks/initial_prompt.toml` — sends a session's initial prompt into the
  pane once the CLI's input box is visible, or on every `plect up` when
  `repeat = "true"`. Contains the prompt-glyph readiness detection and
  submit-backoff behavior described below.
- `channels/tmux_send_keys.toml` — an event channel that types a later event
  into the same pane (for a CLI runtime with no structured delivery
  transport). Shares the readiness/backoff logic with `initial_prompt.toml`.
- `bin/plect-agent-activity` — a fire-and-forget hook script an agent CLI's
  own hook config calls at its own turn boundaries (user prompt submitted /
  turn finished) to self-report `working`/`waiting` via
  `plect state set-message`.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --revision <tag-or-commit>
plect plugin add official/agent/runtime
```

## Why this exists as its own plugin

Both the interactive Claude Code TUI and the interactive Codex TUI run in a
tmux pane with the same input-box glyphs (`›`/`❯`) and the same two race
conditions:

- **Boot readiness**: the pane's shell and the CLI's own startup can together
  take longer than a short fixed sleep, so an instruction sent too early
  types into a shell prompt instead of the CLI.
- **Submit race**: an Enter sent in the same burst as pasted text, or too
  soon after a long paste, is swallowed as a newline instead of acting as
  submit — the CLI's paste-burst detection window can still be open.

`initial_prompt.toml` and `tmux_send_keys.toml` both solve these the same
way (poll for the input-box glyph, then resend Enter with backoff until the
box reads empty), so that logic lives once, here.

## Not included

- Which CLI a session launches (`agent/claude`, `agent/codex`) and its
  model/effort defaults are that plugin's concern, not this one's.
- Any workflow composing this task/channel pair with a resource provider
  (e.g. GitHub) — workflows compose across plugins and stay out of any
  single plugin for now.
