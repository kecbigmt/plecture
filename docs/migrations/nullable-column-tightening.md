# Nullable column tightening migration

This release rebuilds the `sessions`, `node_executions`, `node_execution_layers`,
`task_instances` and `task_instance_layers` tables in `storage.db` (schema
version `20261108120000`). It applies automatically the first time any plect
process opens the store, like every storage migration.

What the stored data looks like afterwards:

- A map column every row carries (`inputs_json`, `outputs_json`, `state_json`,
  `locals_json`, `env_json`) is never `NULL`; no entries is `{}`.
- A layer's `heartbeat_ticks` and `heartbeat_escalations` are never `NULL`; an
  existing `NULL` becomes `0`.
- A layer's `setup_at` is never `NULL`. An existing `NULL` takes the owning
  node execution's or task instance's `setup_at`.
- `node_executions.done_when_json` is `NULL` for a node without a `done_when`,
  never the JSON text `null`.
- `node_executions.name` no longer exists. Nothing ever wrote a value to it.
- `sessions.health_unevaluable_cycles` is `0`, not `NULL`, for every session
  that has a health state.

Every session reads back exactly as it did before the upgrade.

## Before upgrading

Follow [Storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md),
"Before upgrading". It stops every plect process and makes sure the automatic
pre-migration backup can be written.

## Upgrade

Start the new release. On success, stderr shows the backup line and the store
is at `20261108120000`:

```sh
plect storage backups
```

### If the upgrade is refused

The upgrade refuses, and applies no migration at all, when a layer has no
`setup_at` and neither does the node execution or task instance that owns it.
No true value can be derived for such a layer. The error names each table and
row count:

```text
persistence: upgrade refused, no migration applied: node_execution_layers.setup_at is NULL in 2 rows whose node_executions row has no setup_at to copy; set or delete those rows with the previous release, then upgrade; see docs/migrations/nullable-column-tightening.md
```

A store whose schema predates node executions reports `node_instance_layers`
and `node_instances` instead. The store is unchanged and the previous release
still opens it.

To fix it, keep running the previous release and stop every plect process.
Then copy the data home as a backup:

```sh
DATA_DIR="${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}"
cp -a "$DATA_DIR" "$DATA_DIR.backup.$(date -u +%Y%m%dT%H%M%SZ)"
```

List the refused rows with any SQLite client:

```sql
SELECT o.session_id, o.node_id, o.status, l.position, l.effect_id
FROM node_execution_layers l JOIN node_executions o ON o.id = l.execution_id
WHERE l.setup_at IS NULL AND o.setup_at IS NULL;

SELECT o.session_id, o.instance_name, o.status, l.position, l.effect_id
FROM task_instance_layers l JOIN task_instances o ON o.id = l.task_instance_id
WHERE l.setup_at IS NULL AND o.setup_at IS NULL;
```

For each row, do one of the following:

- **The owner is `cleaned`** (its effects are already released): the layer row
  is history only. Delete it:

  ```sql
  DELETE FROM node_execution_layers
  WHERE setup_at IS NULL
    AND execution_id IN (SELECT id FROM node_executions WHERE setup_at IS NULL AND status = 'cleaned');
  DELETE FROM task_instance_layers
  WHERE setup_at IS NULL
    AND task_instance_id IN (SELECT id FROM task_instances WHERE setup_at IS NULL AND status = 'cleaned');
  ```

- **The owner is not `cleaned`:** release it with the previous release first
  (`plect destroy <session>`), then delete the row as above.
- **You know when the layer was set up**, for example from the session's
  events: write that time in the store's timestamp format
  (`2026-09-07T10:00:01.000000000Z`, UTC with nine fractional digits) to the
  owner's `setup_at` or the layer's own. Do not invent a time.

Run the two listing queries again. When they return no rows, start the new
release.

## Rollback

Follow [Storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md),
"Rollback". `plect storage restore` puts back the backup taken just before this
migration, which the previous release opens. The same limit applies: roll back
only before the new release has written anything, because a restore discards
everything written after the backup.
