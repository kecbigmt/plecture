# Catalog registration `dir` → `subdir` migration

This migration covers the `catalogs.toml` and `plect.lock` field rename that
replaced the catalog registration field `dir` with `subdir`. The two files
are the only persisted state affected: the field's meaning, default (empty
means the source root itself), and containment validation are unchanged —
only its name changed.

The change is intentionally breaking. Plecture is pre-1.0, so operators
should migrate persisted data once instead of relying on a compatibility
shim. A binary built after this change fails loud on an unknown `dir` key
in either file, and rejects the old `--dir`/`--catalog-dir` flags as
unknown flags.

## Backup

Before editing either file, stop running plect processes and copy the
current files to a timestamped backup directory:

```bash
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/plect"
BACKUP_DIR="$CONFIG_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$CONFIG_DIR/catalogs.toml" "$BACKUP_DIR/catalogs.toml" 2>/dev/null
cp "$CONFIG_DIR/plect.lock" "$BACKUP_DIR/plect.lock" 2>/dev/null
```

Adjust the path if the process runs with an explicit `--config-home`.

## Field rename

In both files, every `[[catalogs]]` table's `dir = "..."` key becomes
`subdir = "..."`; an entry with no `dir` line needs no change (the field
was already optional and stays optional). Apply the same rename to both
files:

```bash
sed -i 's/^dir = /subdir = /' "$CONFIG_DIR/catalogs.toml"
sed -i 's/^dir = /subdir = /' "$CONFIG_DIR/plect.lock"
```

If either file does not exist (no catalogs registered yet, or nothing
locked yet), skip that `sed` — there is nothing to migrate in it.

## Verification

After migration:

```bash
rg '^dir = ' "$CONFIG_DIR/catalogs.toml" "$CONFIG_DIR/plect.lock"
plect catalog list
```

The `rg` command should print nothing for migrated files (an absent file
is fine — `rg` reports no matches for it too). `plect catalog list` should
show every previously registered catalog with its subdirectory intact
under the `SUBDIR` column, and no drift errors.

## Rollback

Stop plect processes, then restore the copied files:

```bash
cp "$BACKUP_DIR/catalogs.toml" "$CONFIG_DIR/catalogs.toml"
cp "$BACKUP_DIR/plect.lock" "$CONFIG_DIR/plect.lock"
```

Restart plect only after the restore is complete, and use a plect binary
built before this change — the restored files still use the old `dir`
field name.
