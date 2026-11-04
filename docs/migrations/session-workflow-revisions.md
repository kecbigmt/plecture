# Session workflow revision migration

The first ordinary plect command or plect-web startup after this upgrade expands
each live session's workflow from the trusted configuration available at that
moment and assigns a stored revision. The database schema migration adds the
revision table, a nullable session reference, and a one-time completion marker
first; the application fills the references and marks completion before serving
requests. New sessions store their provider revision before workspace setup, so
a failed setup can be retried or cleaned up with `plect destroy <session>` even
if the provider declaration changes. A session without a revision does not
restart the migration and can be removed with `plect destroy --force <session>`.

A session whose workflow or referenced
declaration cannot be resolved stops the migration with its name in the error.
Restore the missing trusted declaration from backup and retry. Do not replace
an unresolved session with a partial definition.

## Backup and cutover

Stop the resident process, plect-web, and every plect command that could change
session state. Choose a backup directory outside the data and configuration trees.
Copy the complete data directory, the global configuration directory, every
configured plugin directory, and every workspace overlay that an existing
session uses. For default locations on a Unix host:

```bash
backup_dir="$(mktemp -d)"
cp -a "${XDG_DATA_HOME:-$HOME/.local/share}/plect" "$backup_dir/data"
cp -a "${XDG_CONFIG_HOME:-$HOME/.config}/plect" "$backup_dir/config"
# Copy configured plugin directories and session workspace overlays here.
```

If plect uses `--data-home` or `--config-home`, copy those directories instead.
Keep the configuration fixed until the first upgraded `plect ls` completes.
That command runs the database migration and assigns revisions. Then restart
the resident process.

## Verification

Run `plect ls` and `plect status <session>` and verify that live sessions show
an eight-character revision digest. Afterward, a configuration edit affects
new sessions; existing sessions continue to use their assigned revision.
Until `plect workflow rebase` is available, destroy and recreate a session to
adopt the new definition.
