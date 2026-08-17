# `[terminal]` table migration

This migration covers the Terminal Operation Surface decided in
`docs/adr/2026-08-17-plugin-boundary-contracts.md` and specified by
`docs/design/plugin-boundary-contracts.md`: a task's top-level `attach` and
`capture` keys move under a `[terminal]` table, joined by two new required
members, `send_text` and `send_keys`. `plect attach` / `plect capture` and
the new `{{terminal "..."}}` template helper (task hooks and channel
argument rendering) all resolve through this one table.

This repository's own shipped catalog (the `session/runtime` plugin's
`tmux` task, and the `tmux_send_keys` channel that used to call `tmux`
directly) is already migrated as part of this change. This migration is for
a user-owned task definition or workspace-dir overlay that declares
top-level `attach` and/or `capture` — a `[terminal]` declaration copied from
the shipped `tmux` task, or written by hand for a different multiplexer.

The change is intentionally breaking. Plecture is pre-1.0, so task authors
migrate their own config once instead of relying on a compatibility shim
that reads both shapes. A task definition still declaring a top-level
`attach` or `capture` key fails to load with a clear error naming the
migration, rather than silently loading with that key dropped.

## Backup

Before editing anything, copy each affected task definition file to a
timestamped backup:

```bash
for TASK_FILE in ~/.config/plect/tasks/*.toml .plect/tasks/*.toml; do
  [ -f "$TASK_FILE" ] || continue
  cp "$TASK_FILE" "$TASK_FILE.migration-backup.$(date -u +%Y%m%dT%H%M%SZ)"
done
```

Substitute your actual task directories (a plugin's `config/tasks/`, the
global `~/.config/plect/tasks/`, or a repo overlay's `.plect/tasks/`) for
the globs above.

## Move `attach`/`capture` under `[terminal]`, add `send_text`/`send_keys`

For each task file declaring top-level `attach` and/or `capture`, move them
into a `[terminal]` table and add the two new required members. All four
members are required together — a table declaring fewer than all four fails
to load, naming the missing member(s).

Before:

```toml
scope       = "run"
attach      = "tmux attach -t {{.Self.session_name}}"
capture     = "tmux capture-pane -p -t {{.Self.session_name}}"
healthcheck = "tmux has-session -t {{.Self.session_name}}"
setup       = "..."
cleanup     = "..."
```

After — `[terminal]` must come after every top-level key in the file, since
TOML scopes every bare `key = value` line following a table header to that
table:

```toml
scope       = "run"
healthcheck = "tmux has-session -t {{.Self.session_name}}"
setup       = "..."
cleanup     = "..."

[terminal]
attach     = "tmux attach -t {{.Self.session_name}}"
capture    = "tmux capture-pane -p -t {{.Self.session_name}}"
send_text  = "tmux send-keys -t {{.Self.session_name}} -- \"$1\""
send_keys  = "tmux send-keys -t {{.Self.session_name}} \"$1\""
```

`send_text` receives the literal text to type as its first shell positional
parameter (`$1`); `send_keys` receives a key token (e.g. `Enter`) the same
way. `attach` and `capture` receive no operand, unchanged from before this
migration. If your multiplexer has no notion of "send a key combo"
independent of "send text", the two can share the same underlying command
as long as each still accepts a single positional argument.

A task file that only ever declared `capture` (with no `attach`) needs the
same treatment: `[terminal]` has no partial form, so `attach` and
`send_text`/`send_keys` must be filled in too, even if `attach` is not
something you use in practice — it can point at a trivial `exit 1 # not
attachable` command if genuinely unsupported, as long as it renders.

## Update consumers wired to the old output shape

If a workflow or channel referenced the old `tmux_send_keys.toml`-style
pattern — hardcoding a multiplexer's send-keys command against
`.Nodes.<id>.outputs.session_name` — replace it with `{{terminal
"send_text"}}` / `{{terminal "send_keys"}}` / `{{terminal "capture"}}` in
the channel's own `args`, and drop the now-unnecessary `.Inputs.session`
wiring: the helper resolves against the session's own plan automatically,
so the channel no longer needs to be told which node owns the terminal.

## Verification

Confirm the migrated task still loads:

```bash
plect workflow show <workflow-id>
```

Confirm `plect attach` / `plect capture` still resolve for a session using
the migrated task:

```bash
plect up <session>
plect attach <session>   # or: plect capture <session>
```

## Rollback

Restore each task definition file from its backup, and use a plect binary
built before this change — the restored top-level `attach`/`capture` shape
is a load error under a post-migration binary.

```bash
mv "$TASK_FILE.migration-backup.<timestamp>" "$TASK_FILE"
```
