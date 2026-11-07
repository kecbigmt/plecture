# Storage upgrade backup and rollback

Use this procedure for any release that migrates the `storage.db` schema. The
release applies the migration automatically the first time any plect process
opens the store (`plect serve`, `plect-web`, or any command). It also takes the
backup that a rollback restores. See
[the design](../design/sqlite-persistence.md#pre-migration-backups-and-restore)
for the full behavior.

## Before upgrading

1. Stop every plect process that uses the data home: the resident
   `plect serve`, `plect-web`, and any running command.
2. Leave `PLECT_STORAGE_BACKUP_RETAIN` unset, which keeps 3 backups, or set it
   to 1 or more. A value of `0` turns the backup off, and then the only way to
   roll back is a copy you made yourself.
3. Confirm that the filesystem holding the data home has room for one more
   copy of `storage.db`, plus the retired sidecars if the data home still has
   any (`pending_delivery.json`, `events/`).

Some releases predate automatic backups: `plect storage --help` does not
list `backups` for them. When upgrading to such a release, copy the data home
yourself while every process is stopped:

```sh
DATA_DIR="${PLECT_DATA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/plect}"
cp -a "$DATA_DIR" "$DATA_DIR.backup.$(date -u +%Y%m%dT%H%M%SZ)"
```

## Upgrade

Start the new release. The first process that finds the store behind copies it
to `<data home>/backups/<UTC timestamp>-v<from version>/`, then applies the
migration. It prints the result on stderr:

```text
plect: storage: backed up schema <from> to <backup path> before migrating to <to>
```

If the backup cannot be written, the process refuses to start and applies no
migration. The store is left unchanged. Fix the cause (usually disk space or
permissions) and start the process again.

If the migration itself fails, `storage.db.migration.json` records the error
and the backup path, under `error` and `backup`.

Confirm that the backup exists:

```sh
plect storage backups
```

## Rollback

You may roll back only right after the upgrade, before anything has been
written to the new schema. Migrations are forward-only (see
[the forward-only migrations decision](../adr/2026-11-07-forward-only-migrations.md)).
A restore discards everything written since the backup. Once the new release
has done real work, fix forward with a new release instead.

1. Stop every plect process that uses the data home.
2. List the backups and choose the newest one whose schema is the earlier
   release's:

   ```sh
   plect storage backups
   ```

3. Restore it. Either binary can run this command: the new release, or the
   earlier one if it has `plect storage restore`.

   ```sh
   plect storage restore <name>
   ```

   The command refuses in three cases, and changes nothing when it does:
   - a plect process still has the store open;
   - no backup has that name;
   - the backup's schema is newer than the binary running the command
     supports.

4. Start the earlier release.

The restore moves the files it replaced into
`<data home>/restore-displaced/<UTC timestamp>/`: `storage.db`, its
`-wal`/`-shm`/`-journal` siblings, any retired sidecars, and
`storage.db.migration.json`. The backup itself stays where it was. Delete the
displaced directory once the rollback is confirmed. Nothing prunes it
automatically.

If the backup was a manual copy of the data home, stop every process, move the
data home's contents aside, and copy the backup's contents back in.
