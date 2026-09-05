# Standing session dispatch dialect migration

The configuration dialect advances from version 1 to version 2 for observer
queries, workflow populations, and virtual-root capacity. Existing declarations
retain their meaning; trees that do not use the new surfaces only need their
dialect declarations updated. Plecture is pre-1.0, so the loader accepts one
dialect rather than carrying a compatibility path.

## Backup

Stop running `plect` processes, then copy the complete config home before
editing it:

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP="$CONFIG_HOME.migration-backup.$STAMP"
cp -a "$CONFIG_HOME" "$BACKUP"
```

If editable plugin directories or a separately managed catalog live outside
the config home, back those directories up as well.

## Update dialect declarations

Change `schema_version = 1` to `schema_version = 2` in the user tree's
`config.toml`, `catalogs.toml`, and `plect.lock`. Add
`schema_version = 2` to a hand-authored `config.toml` that lacks a declaration.
Change the same declaration in every editable plugin's `plugin.toml` and in
any editable catalog manifest.
Catalog-managed plugins are updated through their catalog rather than edited
inside the cache.

```bash
rg -n '^schema_version *= *1$' "$CONFIG_HOME"
```

Each reported dialect declaration must be updated. Do not alter state.json;
population provenance and evaluator state are additive fields written only
after a version-2 population runs.

## Adopt the new surfaces when needed

No query or population is synthesized by this migration. An observer that
enumerates or streams resource appearances declares `[<id>.query]` with shared
`inputs_schema` and `item_schema` contracts and at least one of `poll` and
`subscribe`. A user-owned workflow can then declare
`[[<id>.populations]]` against that observer.

The optional root `max_up_children` setting belongs in `config.toml` and must
be a positive integer. Leave it absent to retain unlimited virtual-root
admission.

## Verification

Confirm that no version-1 declaration remains, then load both configuration
and the resident daemon:

```bash
rg -n '^schema_version *= *1$' "$CONFIG_HOME"
plect workflow list
plect serve
```

The `rg` command produces no output. Stop the verification server after it has
loaded successfully. If populations are configured, inspect their emitted
`plect.workflow_population.*` events using the normal event-log interface.

## Rollback

Stop `plect`, move the migrated tree aside, and restore the backup:

```bash
[ -n "$CONFIG_HOME" ] && [ "$CONFIG_HOME" != "/" ] ||
  { echo "refusing unsafe CONFIG_HOME" >&2; exit 1; }
[ -d "$BACKUP" ] || { echo "backup not found: $BACKUP" >&2; exit 1; }
mv "$CONFIG_HOME" "$CONFIG_HOME.rolled-back.$STAMP"
mv "$BACKUP" "$CONFIG_HOME"
```

Use a dialect-1 binary with the restored tree. The moved migrated tree remains
available for inspection until rollback is confirmed.
