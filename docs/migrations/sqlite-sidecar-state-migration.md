# SQLite sidecar-state migration

This procedure removes post-cutover `tombstone.json`, moves
`chain_attempts.json` sidecars into `storage.db`,
and retires the `events/` tree. The updated binary applies the append-only
schema migration and performs the import when it first opens the database.

## Before upgrading

Stop every plect process that uses the data directory, then follow
[storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md#before-upgrading).
Before applying the schema migration, the updated binary backs up `storage.db`
together with the `pending_delivery.json` and `events/` sidecars. Pending
delivery is imported by the separate
[core subscriptions migration](core-subscriptions.md), which resolves resource
declarations before recording membership.

The importer maps each chain-attempt session name to
its live `sessions.id`, or to the most recently destroyed row of that name.
The legacy chain-attempt key's third component is not persisted, but when it
names an existing session it must name the resolved session; a mismatch stops
the import. Entries that collapse to the same `(session_id, instance, chain_id)`
must have the same fingerprint.
`tombstone.json` carries no fact absent from a retained destroyed row, so the
importer removes it without mapping it. It also accepts and removes the
`log.jsonl`, `.gen`, and `.cursor.<consumer>` files that the
[durable-storage cutover](sqlite-durable-storage-cutover.md) already imported,
including an events-only tombstone that the earlier import intentionally
skipped. A name with neither row in a sidecar that requires import, a malformed
sidecar, a chain-attempt generation that identifies another session, or an
unexpected path in `events/` stops the import and leaves every source sidecar
in place. Correct the source, or restore the backup, before retrying.

## Upgrade and verify

Run any normal command from the updated release. Its storage opener applies
the migration, imports valid rows transactionally, and only then removes the
source sidecars. An interrupted cleanup is safe to rerun because the rows use
primary-key upserts before files are removed.

```sh
DATA_DIR="${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}"
plect status
find "$DATA_DIR" -maxdepth 3 -type f -not -path "$DATA_DIR/backups/*" | sort
```

The listing contains `storage.db`, its SQLite and persistence-gate sidecars,
any `delivery-locks/<escaped-session>.lock` files, and any pending delivery
sidecar awaiting the core subscriptions migration. Files under `backups/`
are left out of the listing on purpose: they are the pre-migration backup.
It contains no `events/` tree, `tombstone.json`, or `chain_attempts.json`.

## Rollback

Follow [storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md#rollback).
The backup taken before the migration holds the sidecars that the import
removed, and `plect storage restore` puts them back along with `storage.db`.
The schema migration is append-only, so restoring the backup is the only
rollback for both the database schema and the removed sidecars.
