# `session/runtime` plugin split migration

This migration covers the plugin split decided in
`docs/adr/2026-08-17-plugin-boundary-contracts.md`: the merged
`session/runtime` plugin dissolves into four flat, independently
selectable plugins — `tmux`, `claude`, `codex`, `slack` — and `gh-guard`
moves into the `github` plugin. `plugins/catalog.toml` lists the flat set;
`session/runtime` is gone.

This covers anyone with `official/session/runtime` (or an equivalent alias)
enabled via `plect plugin add` / `plect catalog add`, and anyone whose own
workflow or task overlay wires the task/channel/input names this split
renames.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate their own config once instead of relying on a compatibility shim
that reads both the merged and split layouts.

## Backup

Before touching anything, back up the plugin-enablement state files:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp "$CONFIG_HOME/catalogs.toml" "$CONFIG_HOME/catalogs.toml.migration-backup.$STAMP"
cp "$CONFIG_HOME/plect.lock" "$CONFIG_HOME/plect.lock.migration-backup.$STAMP"
```

Also back up any workflow or task file you own that references one of the
renamed names below:

```bash
for DIR in "$CONFIG_HOME/workflows" "$CONFIG_HOME/tasks" "$CONFIG_HOME/channels"; do
  [ -d "$DIR" ] || continue
  cp -r "$DIR" "$DIR.migration-backup.$STAMP"
done
```

Substitute your own repo-overlay `.plect/` directories (above the
workspace dir) for the global config paths above if you keep workflow/task
overlays there instead.

## Swap the enabled plugin

Disable `session/runtime`, enable the four plugins that replace it (and
`github`, if you use `gh-guard` and don't already have it):

```bash
ALIAS=official   # substitute your own catalog alias if different

plect plugin remove "$ALIAS/session/runtime"
plect plugin add "$ALIAS/tmux"
plect plugin add "$ALIAS/claude"    # only if your workflows use Claude Code
plect plugin add "$ALIAS/codex"     # only if your workflows use Codex
plect plugin add "$ALIAS/slack"     # only if your workflows deliver to Slack
plect plugin add "$ALIAS/github"    # only if you use gh-guard
```

`plect plugin add`/`remove` update `plect.lock` for you — do not hand-edit
its `[[plugins]]` entries.

## Update renamed task/channel/input references

If a workflow file you own (`[[nodes]]` / `[[event.channel]]`) references
any of these names, update it:

| Old name | New name | Kind | Notes |
|---|---|---|---|
| `initial_prompt` | `claude_initial_prompt` or `codex_initial_prompt` | task | Pick the one matching your agent; each plugin now owns its own copy (task ids are unique across the whole mounted catalog). |
| `tmux_send_keys` | `terminal_submit` | channel | Now shipped by `codex`, not a shared plugin — see `docs/migrations/no-channel-server-claude-migration.md` if you delivered to a *Claude* pane with this channel. |
| `gh_guard` (boolean task input, `"1"`/unset) | `path_prepend` (string task input, a directory) | task input on `claude`/`codex`/`codex_exec` | See below. |

Example — before:

```toml
[[nodes]]
uses = "claude"
inputs.tmux_session = "{{.Nodes.tmux.outputs.session_name}}"
inputs.gh_guard      = "1"
```

Example — after: wire the `github` plugin's own `gh_guard` task and pass
its output as `path_prepend`:

```toml
[[nodes]]
uses = "gh_guard"

[[nodes]]
uses = "claude"
inputs.tmux_session = "{{.Nodes.tmux.outputs.session_name}}"
inputs.path_prepend = "{{.Nodes.gh_guard.outputs.dir}}"
```

If your workflow used `initial_prompt` with `inputs.tmux_session`, the
renamed task no longer reads that input's value directly (it now resolves
the terminal automatically via `{{terminal "..."}}`) — but keep the input
wired anyway, unchanged, since it still forces the correct DAG ordering:

```toml
[[nodes]]
uses                = "claude_initial_prompt"   # was "initial_prompt"
inputs.tmux_session = "{{.Nodes.tmux.outputs.session_name}}"
inputs.template      = "{{get .SessionInputs \"template\" \"\"}}"
```

## Update any `{{bin ...}}` references to renamed executables

If a user-owned task overlay references one of the `plect`-prefixed
executables directly (rather than through the shipped tasks, which already
carry the update), rename it:

| Old | New |
|---|---|
| `plect-claude-agent-activity` | `claude-agent-activity` |
| `plect-codex-agent-activity` | `codex-agent-activity` |
| `plect-codex-exec-worker` | `codex-exec-worker` |
| `plect-codex-exec-enqueue` | `codex-exec-enqueue` |

## Verification

Confirm the new plugins are enabled and loadable:

```bash
plect plugin list
plect plugin verify
```

Confirm a workflow using the renamed tasks/channels still resolves:

```bash
plect workflow show <workflow-id>
```

Confirm an end-to-end session still comes up and delivers:

```bash
plect up <session>
plect event publish <session> --type user.emit --body "test delivery"
plect capture <session>
```

## Rollback

Restore the backed-up state files and workflow/task directories, and use a
plect binary built before this change — the restored `session/runtime`
plugin id is invisible to a post-migration binary's catalog:

```bash
cp "$CONFIG_HOME/catalogs.toml.migration-backup.$STAMP" "$CONFIG_HOME/catalogs.toml"
cp "$CONFIG_HOME/plect.lock.migration-backup.$STAMP" "$CONFIG_HOME/plect.lock"
for DIR in "$CONFIG_HOME/workflows" "$CONFIG_HOME/tasks" "$CONFIG_HOME/channels"; do
  [ -d "$DIR.migration-backup.$STAMP" ] || continue
  rm -rf "$DIR"
  mv "$DIR.migration-backup.$STAMP" "$DIR"
done
```
