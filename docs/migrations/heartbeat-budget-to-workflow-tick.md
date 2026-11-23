# Heartbeat budget moves to the workflow tick

The bound on how long a task instance's `done_when` may stay unmet moves from
the task to the workflow that runs it:

- A task no longer declares `[<task>.budget]` (`heartbeat_budget`,
  `on_exhaust`). Declaring it is a load error, `PLECTURE-CFG-FIELD-UNKNOWN`,
  that names this procedure.
- A `done_when` holds only its `all` leaves. A `budget` or `heartbeat_budget`
  key inside `done_when` is a load error, and `plect task setup
  --done-when-json` refuses any top-level key other than `all`.
- A workflow declares `[<workflow>.tick] heartbeat_budget = <n>`, a positive
  integer. It applies to every task instance with a `done_when` in a session
  that workflow produces. Omitting it means unbounded; `0` is a load error.

Two behaviours change for a host that does nothing beyond removing the task
tables:

- The loader used to give every task with a `done_when` and no `budget` a
  budget of 3. Omission now means unbounded, as the language reference always
  stated. A workflow that relied on that default escalates only once it
  declares `heartbeat_budget`.
- A running session reads its budget from the workflow revision it is pinned
  to. A revision recorded before this upgrade carries no workflow budget, so
  the session runs unbounded until it is rebased onto a workflow that declares
  one.

The change is intentionally breaking. Plecture is pre-1.0, so hosts migrate
their configuration once rather than relying on a compatibility read.

## Backup

Stop the resident process (`plect serve`), plect-web, and any plect command
that could change session state. Then copy the configuration and data
directories somewhere outside both trees:

```bash
BACKUP_DIR="$(mktemp -d)"
cp -a "${XDG_CONFIG_HOME:-$HOME/.config}/plect" "$BACKUP_DIR/config"
cp -a "${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}" "$BACKUP_DIR/data"
```

If plect runs with `--config-home` or `--data-home`, copy those directories
instead. Copy any repo overlay (`.plect/`) and any locally authored plugin's
`config/` the same way.

## Procedure

1. Install the upgraded `plect`, and update each catalog to a revision whose
   shipped tasks no longer declare `budget`. A Git-sourced catalog takes an
   explicit revision: the release tag of the upgraded `plect`, or any later
   commit, since every such revision contains the budget removal:

   ```bash
   plect catalog update <alias> --revision <release-tag-or-commit>
   ```

   A path-sourced catalog takes no revision; update the checkout it points at
   first, then re-resolve it:

   ```bash
   plect catalog update <alias>
   ```

2. Find every task table that still declares a budget, in every layer you
   own:

   ```bash
   rg -n '^\[[^]]+\.budget\]|heartbeat_budget|on_exhaust' \
     "${XDG_CONFIG_HOME:-$HOME/.config}/plect" .plect
   ```

   Delete each `[<task>.budget]` table (and any `budget` or `heartbeat_budget`
   inside a `done_when`). `on_exhaust` has no replacement: nothing ever read
   it, and exhaustion always escalates.

3. For each workflow that should keep escalating, add `heartbeat_budget`
   beside its `heartbeat`. To keep the behaviour of the previous release, use
   `3` for every workflow whose sessions run a task with a `done_when`: tasks
   that declared `heartbeat_budget = 3` and tasks that declared no budget were
   both bounded at 3.

   ```toml
   [issue_worker.tick]
   on               = ["resource.*"]
   heartbeat        = "15m"
   heartbeat_budget = 3
   ```

   Leave it out of a workflow whose sessions should wait indefinitely, such as
   one that runs a standing goal.

4. Rebase live sessions so that they adopt the edited workflows. A change to
   `[tick]` does not require a restart:

   ```bash
   plect workflow rebase --all
   ```

5. Restart the resident process.

## Verification

```bash
rg -n '^\[[^]]+\.budget\]' "${XDG_CONFIG_HOME:-$HOME/.config}/plect" .plect
plect status <session>
```

The `rg` command prints nothing. For a session whose workflow declares a
budget, `plect status` shows `heartbeat budget <ticks>/<budget>` on each task
instance with a `done_when`.

## Rollback

Stop plect, reinstall the previous release, and restore the copied
directories:

```bash
rm -rf "${XDG_CONFIG_HOME:-$HOME/.config}/plect"
cp -a "$BACKUP_DIR/config" "${XDG_CONFIG_HOME:-$HOME/.config}/plect"
rm -rf "${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}"
cp -a "$BACKUP_DIR/data" "${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}"
```

The previous release refuses `heartbeat_budget` in a workflow's `[tick]`, so
restore the configuration together with the binary.
