# Healthcheck and heartbeat-budget migration

> The `healthcheck` scalar, `movement_signal`, and `[tick.movement_source]`
> this procedure produces are themselves retired. Apply
> [`health-table-migration.md`](health-table-migration.md) after this one.

This migration covers the state and workflow shape that replaced the old
fact-change round budget.

The change is intentionally breaking. Plecture is pre-1.0, so operators should
migrate persisted data once instead of relying on compatibility shims.

## Backup

Before editing any data directory, stop running plect processes and copy the
current files to a timestamped backup directory:

```bash
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/plect"
BACKUP_DIR="$DATA_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$DATA_DIR/state.json" "$BACKUP_DIR/state.json"
tar -C "$CONFIG_DIR" -cf "$BACKUP_DIR/config-toml.tar" workflows tasks
```

Adjust the paths if the process runs with explicit `--data-dir` or config
locations.

## State Changes

For each session in `state.json`:

- Move `watchdog.checked_at` to `health.last_checked_at` when present.
- Move `progress.fingerprint` to `health.last_fingerprint` when present.
- Move `progress.observed_at` to `health.last_movement_at` when present.
- Remove the top-level `watchdog` object.
- Remove the top-level `progress` object.
- Keep `tick_backoff`; heartbeat budget ticks still follow quiet backoff.

For each task's `done_when` state:

- Remove `rounds`.
- Remove `last_auto_revival_revision`.
- Add or leave unset `heartbeat_ticks`.
- Add or leave unset `heartbeat_escalations`.

An unset heartbeat counter is equivalent to zero.

## Workflow And Task Config Changes

In workflow files, add a dedicated healthcheck table when the workflow needs
non-default stall detection:

```toml
[healthcheck]
period = "5m"
stall_threshold = "15m"
renotify_every = 3
```

Defaults are `period = "5m"`, `stall_threshold = "15m"`, and
`renotify_every = 3`.

In task definitions, replace the done_when budget key:

```toml
[done_when.budget]
heartbeat_budget = 3
```

Remove the old `max_rounds` key.

Rename movement declarations:

- `[tick.progress_source]` becomes `[tick.movement_source]`.
- `progress_signal = "..."` becomes `movement_signal = "..."`.
- Signal JSON should emit `movement_expected`, not `progress_expected`.

Remove `stall_observe` relay tasks and workflow nodes whose only purpose was
to sample stalls. The healthcheck cycle now owns that sampling in core.

## Verification

After migration:

```bash
rg 'watchdog|progress_source|progress_signal|progress_expected|last_progress_at|max_rounds|last_auto_revival_revision|"rounds"' "$DATA_DIR/state.json" "$CONFIG_DIR"
plect ls
plect status <session>
```

The `rg` command should print nothing for migrated files. `plect status`
should show `last_checked_at` and, when movement has been observed,
`last_movement_at`.

## Rollback

Stop plect processes, then restore the copied files:

```bash
cp "$BACKUP_DIR/state.json" "$DATA_DIR/state.json"
tar -C "$CONFIG_DIR" -xf "$BACKUP_DIR/config-toml.tar"
```

Restart plect only after the restore is complete.
