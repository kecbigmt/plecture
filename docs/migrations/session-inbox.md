# Session inbox migration

This procedure moves the events a headless runtime has not processed from the
plugin's queue directory into `storage.db`, and moves existing sessions onto
the declarations that take from the session inbox. It applies to sessions
whose runtime is the `claude` plugin's `headless_runtime` or the `codex`
plugin's `exec_runtime`. Sessions with a pane runtime need no step of their
own. See [core owns the session inbox](../adr/2026-11-20-core-session-inbox.md)
and
[headless runtimes take their turns from the session inbox](../adr/2026-11-20-headless-runtimes-take-from-inbox.md).

The schema migration adds the `inbox_entries` table. It is forward-only and
adds no row.

## Before upgrading

1. Stop every plect process that uses the data home: the resident
   `plect serve`, `plect-web`, and any running command. Stop the headless
   workers too, by bringing their sessions down or by stopping their panes: a
   worker of the old release that keeps draining its queue directory while
   the importer reads it would run an entry that is also migrated.
2. Follow
   [storage upgrade backup and rollback](storage-upgrade-backup-and-rollback.md#before-upgrading).
   The importer below takes its own backup as well.

## Edit workflows

A binding of `headless_delivery` or `exec_delivery` no longer takes inputs.
Remove its `inputs` table:

```toml
[[my_workflow.event.channel]]
name    = "runtime"
uses    = "official.codex.exec_delivery"
include = ["plect.instruction", "resource.*"]
```

- `queue_dir` and `enqueue_timeout` are removed with no replacement.
- `message_envelope` moves to the inputs of the node that uses
  `headless_runtime` or `exec_runtime`. Its placeholders and default are
  unchanged.
- `headless_runtime` no longer reports `queue_dir` or `socket_path`, and
  `exec_runtime` no longer reports `queue_dir`. Remove every value that reads
  them. A sender that dialed a headless session's socket publishes an event
  to the session with `plect event publish` or through a declared service.

## Upgrade and import

Install the new binary and update the `claude` and `codex` plugins with
`plect plugin update`. Then run the importer from a checkout of this release.
Pass the absolute path to the new `plect` binary, because `PATH` may still
select the old release:

```bash
set -o pipefail
python3 scripts/migrate-session-inbox.py \
  --data-home /path/to/plect-data \
  --plect /path/to/new/plect 2>&1 | tee /path/to/import.log
```

The importer:

1. Makes an online backup of `storage.db` and copies every queue directory a
   live session's nodes reported, with its quarantine directory, into
   `<data-home>/session-inbox-migration-backups/<timestamp>/`.
2. Runs `plect --data-home <data-home> storage migrate`.
3. Inserts one inbox entry for each pending or claimed queue file that carries
   the id of an event in the session's log, in one transaction.
4. Renames each migrated file to `<name>.migrated`, so a second run does not
   hand it over again.

It prints one line per file. Review every `Not migrated:` and `Left in place:`
line in `import.log` before restarting:

| Line | Meaning | What to do |
|---|---|---|
| `Not migrated: … carries no event id` | A message written by the removed `channel-server queue` bridge. It never was an event. | Read the file and publish its text to the session if it still matters. |
| `Not migrated: … unparsable` | A corrupt queue file. | Inspect the backup copy. |
| `Not migrated: … is not in the log` | The file names an event the session's log does not hold. | Inspect the backup copy. |
| `Not migrated: … no channel binding names a queue directory` | The session's stored workflow has no binding that fed the queue. | Publish the event again after the upgrade if it still matters. |
| `Left in place: …` | A parked batch, a leaked completion directory, or a quarantine directory. None of them was pending. | Delete it with the queue directory below. |

## Rebase sessions

A session executes the declarations stored in its revision, and those still
name the removed enqueue executable. Rebase before starting the resident
process: a dispatcher that ran on the old revision would fail each delivery to
the runtime and record it as a `plect.channel.error`. The command does not
need the resident process.

```bash
plect workflow rebase --all --dry-run
plect workflow rebase --all --with-restart
```

The runtime effect changed, so the class is `restart`: an up session is
brought down on the old revision and up on the new one. Start the resident
process afterwards. A worker then takes the migrated entries as its first
batch.

## Verify and clean up

- `plect event list <session> --type plect.channel.error` shows no new
  delivery failure for the runtime binding.
- Publish a harmless event to one headless session and check that its runtime
  answers it once.
- After the sessions run on the new revision, delete each old queue directory
  and its `.quarantine` sibling. Their paths are in the `source` files of the
  importer's backup.

## Roll back

Keep the resident process stopped. Restore the `storage.db` copy from the
importer's backup directory with the
[rollback procedure](storage-upgrade-backup-and-rollback.md#rollback) of the
old release, rename every `*.json.migrated` file back to `*.json`, restore the
workflow files, and reinstall the old binary and plugins. The rollback removes
every session and event created since the backup.
