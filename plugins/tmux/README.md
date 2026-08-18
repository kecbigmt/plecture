# tmux

The tmux-backed interactive endpoint: a task that creates/attaches/tears
down a tmux pane, plus the `[terminal]` table (`attach`/`capture`/
`send_text`/`send_keys`) that lets any other plugin's task hook or channel
reach it through `{{terminal "..."}}` without knowing tmux is behind it —
see `docs/design/plugin-boundary-contracts.md`'s Terminal Operation
Surface.

## Contents

- `config/tasks/tmux.toml` — creates/attaches/tears down the tmux pane an
  agent CLI runs in. `[terminal].attach`/`.capture` let `plect attach`/
  `plect capture` reach the pane; `[terminal].send_text`/`.send_keys` are
  consumed by agent-runtime plugins (`claude`, `codex`) via
  `{{terminal "..."}}`. `[health].alive` is a plain `tmux has-session`, and
  `[health].activity` fingerprints the pane's visible contents so any
  workload running in it, agent or not, contributes activity evidence.

No executables: every `[terminal]` verb is inline shell in `tmux.toml`, so
this plugin ships config only.

## Install

```bash
plect catalog add official git+https://github.com/kecbigmt/plecture --subdir plugins --revision <tag-or-commit>
plect plugin add official/tmux
```

## Not included

- Which agent CLI runs inside the pane, and how it launches/submits/reads
  readiness — a consumer plugin's concern (`claude`, `codex`), composed via
  `{{terminal "..."}}`. This plugin's contract stops at raw terminal
  operations; see the "Put submit and readiness composition in the
  multiplexer plugin" alternative rejected in
  `docs/adr/2026-08-17-plugin-boundary-contracts.md`.
