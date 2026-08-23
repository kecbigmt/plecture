# Task layer effect_id migration

This migration covers renaming `TaskLayerState` (a nesting chain's per-layer
lifecycle record) to `LayerState`, and its `task_id` field to `effect_id`: a
nesting layer runs an effect declaration, not a task, the same distinction
`TaskState.TaskID` already draws for the composed instance itself. See
issue #270.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate persisted data once instead of relying on a permanent Go-field /
JSON-tag mismatch.

Scope: only a session's persisted `layers[].task_id` changes. `TaskState`'s
own `task_id` field (naming which task declaration an instance runs) is
untouched — only a nested instance's per-layer records are affected, and only
for a session with at least one nested task instance.

## Backup

Before editing any data directory, stop running plect processes and copy the
current state file to a timestamped backup directory:

```bash
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
BACKUP_DIR="$DATA_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$DATA_DIR/state.json" "$BACKUP_DIR/state.json"
```

Adjust the path if the process runs with an explicit `--data-dir`.

## State changes

For every task instance's layer record in `state.json`, rename `task_id` to
`effect_id`. The rename is conditional on `has("task_id")` so the command is
safe to run against state that is already migrated, or a `state.json` where
one session migrated and another has not yet — reapplying it, or applying it
to a mixed file, must never overwrite an already-renamed layer's `effect_id`
with `null`:

```bash
jq '(.sessions[]? | .tasks[]? | select(.layers != null) | .layers[]) |=
      (if has("task_id") then (. + {effect_id: .task_id} | del(.task_id)) else . end)' \
  "$DATA_DIR/state.json" > "$DATA_DIR/state.json.new"
```

A session with no nested task instances has no `layers` array on any of its
tasks, so the `select` leaves it untouched.

Before replacing the live file, verify that every layer's identity survived
the transform exactly — the same set of ids, just under the new key, with
none dropped or turned to `null`:

```bash
OLD_IDS=$(jq -S '[.sessions[]? | .tasks[]? | .layers[]? | (.effect_id // .task_id)]' "$DATA_DIR/state.json")
NEW_IDS=$(jq -S '[.sessions[]? | .tasks[]? | .layers[]? | .effect_id]' "$DATA_DIR/state.json.new")
if [ "$OLD_IDS" != "$NEW_IDS" ]; then
  echo "migration verification failed: layer identities changed — not replacing state.json" >&2
  echo "before: $OLD_IDS" >&2
  echo "after:  $NEW_IDS" >&2
  exit 1
fi
mv "$DATA_DIR/state.json.new" "$DATA_DIR/state.json"
```

## Consumer changes

Nothing in `plect status`/`plect ls` display text changes — the field is
internal bookkeeping, not rendered under its own name. A consumer reading
`state.json` directly (a dashboard, a relay task) that matches on
`layers[].task_id` must switch to `layers[].effect_id`.

## Verification

After migration:

```bash
jq '[.sessions[]? | .tasks[]? | .layers[]? | select(has("task_id"))] | length' \
  "$DATA_DIR/state.json"
plect ls
plect status <session-with-a-nested-task>
```

The `jq` command should print `0`. `plect status` on a session with a nested
task instance should show the instance's outputs unchanged.

## Rollback

Stop plect processes, then restore the copied file:

```bash
cp "$BACKUP_DIR/state.json" "$DATA_DIR/state.json"
```

Restart plect only after the restore is complete.

## Rollout note

This PR ships the migration procedure verified against a backup copy of real
state, not executed live — live execution against a running deployment rides
the next scheduled maintenance window alongside the already-pending binary
bump, per the owner's own rollout ruling on issue #270.
