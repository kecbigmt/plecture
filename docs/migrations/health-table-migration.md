# Health table migration

This migration covers the move from three health-input surfaces to one
`[health]` table on the task definition, and the persisted state rename that
follows the activity vocabulary. See
[`../adr/2026-08-18-health-declaration.md`](../adr/2026-08-18-health-declaration.md).

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate config and persisted data once instead of relying on compatibility
shims. Task definitions still declaring the retired keys fail to load with a
named error, so an un-migrated file is loud rather than silently probe-less.

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

## Task config changes

In every task definition — global (`~/.config/plect/tasks/`) and repo overlay
(`.plect/tasks/`) — move the retired scalars into a `[health]` table. The
table is a TOML table, so it must sit after every bare `key = value` in the
file:

```toml
# before
healthcheck    = "kill -0 {{.Self.pid}}"
movement_signal = "my-fingerprint"

# after
[health]
alive    = "kill -0 {{.Self.pid}}"
activity = "my-fingerprint"
```

Both members are optional and independent — declare only the probes the task
actually has. A `[health]` header declaring neither is a load error, as is any
key other than `alive` and `activity`.

If a task declared `movement_signal`, its JSON envelope changes shape: the
`supported` and `movement_expected` booleans collapse into one required
`status` field.

```json
{ "status": "active", "fingerprint": "...", "observed_at": "..." }
```

Translate the old booleans as:

| Old envelope | New `status` |
|---|---|
| `"supported": false` | `none` |
| `"supported": true`, `"movement_expected": false` | `idle` |
| anything else supported | `active` |

`active` is the residual: emit it whenever the probe observed something but
cannot positively establish that quiet is normal. `idle` is the only value
that changes what core concludes, so reach for it only when the probe really
knows the surface is between turns.

`status` has no default. An envelope omitting it, or carrying a value outside
the three, is a parse error naming the value set — the probe contributes no
evidence for that evaluation, exactly as a non-zero exit would.

## Workflow config changes

Remove `[tick.movement_source]` from every workflow file. It has no
replacement at the workflow level: the task that owns the surface declares
`[health].activity` instead.

```toml
# delete this block entirely
[tick.movement_source]
name   = "fingerprint"
script = "..."
```

The shipped `tmux` task now declares a capture-based pane fingerprint, and the
shipped `claude`, `codex`, and `codex_exec` tasks declare their turn-boundary
activity hooks as their activity probe. A workflow composing those tasks gets
stall detection with no wiring of its own, so a user-owned pane-fingerprint
script that `[tick.movement_source]` pointed at is retired along with the
declaration — delete the script once no workflow references it.

The workflow-level `[healthcheck]` table (`period`, `stall_threshold`,
`renotify_every`) is unchanged. It declares the sampling cycle, not what
health means.

## State changes

For each session in `state.json`, under `health`:

- Rename `last_movement_at` to `last_activity_at`.

`last_fingerprint` keeps its name, but its value is now composed only from
task-instance probes. A value carrying the old session-scoped source's
`workflow:` prefix no longer matches anything core produces, so the first
evaluation after migration records a fresh observation:

```bash
jq '(.sessions[]? | select(.health != null) | .health) |= (
      (. + {last_activity_at: .last_movement_at})
      | del(.last_movement_at)
      | if (.last_fingerprint // "") | startswith("workflow:")
        then del(.last_fingerprint) else . end)' \
  "$DATA_DIR/state.json" > "$DATA_DIR/state.json.new" \
  && mv "$DATA_DIR/state.json.new" "$DATA_DIR/state.json"
```

## Consumer changes

`plect status` prints `last_activity_at` where it printed `last_movement_at`.
The JSON surfaces rename alongside it:

- `plect status --json`: `runtime.last_movement_at` → `runtime.last_activity_at`.
- health report JSON: `movement_expected` / `movement_declared` /
  `movement_fresh` / `last_movement_at` → `activity_due` /
  `activity_declared` / `activity_fresh` / `last_activity_at`. The
  expectation field is `activity_due`, not `activity_expected`: it is core's
  own derived accusation, and reusing the retired envelope field's name would
  have kept that name alive on a second surface.
- health escalation metadata: the `last_movement_at` key becomes
  `last_activity_at`.

Update any subscriber, dashboard, or relay task matching on those names.

## Verification

After migration:

```bash
rg 'healthcheck *=|movement_signal|movement_source|movement_expected|activity_expected|last_movement_at|"supported"' \
  "$DATA_DIR/state.json" "$CONFIG_DIR"
plect ls
plect status <session>
```

The `rg` command should print nothing for migrated files. `plect ls` should
show a HEALTH column that is not `undeclared` for a running session, and
`plect status` should show `last_activity_at` once activity has been observed.

## Rollback

Stop plect processes, then restore the copied files:

```bash
cp "$BACKUP_DIR/state.json" "$DATA_DIR/state.json"
tar -C "$CONFIG_DIR" -xf "$BACKUP_DIR/config-toml.tar"
```

Restart plect only after the restore is complete.
