# Move the plugin cache-home override to its parent directory

`PLECT_CACHE_HOME` and `--cache-home` now name plect's cache root. Catalog
snapshots live under `<cache-home>/catalogs/`, and disposable plugin runtime
caches live under `<cache-home>/plugins/<plugin>/`. Previously the override
named the catalog cache directory itself. There is no compatibility path for
the old value.

## Backup

Before upgrading, stop plect and back up the configuration, shell startup
file, or service definition that sets `PLECT_CACHE_HOME` or `--cache-home`.
Record the old override value with that backup. Cache contents are disposable;
copy them only if you want to retain them for inspection.

## Change the override

Set each explicit override to the parent of its old catalog cache directory.
For example, an old value of `/var/cache/plect/catalogs` becomes
`/var/cache/plect`. The default requires no edit: it resolves to
`$XDG_CACHE_HOME/plect`, or `~/.cache/plect` when `XDG_CACHE_HOME` is unset.
The new layout reuses an existing `catalogs/` child when it is present; plect
can rebuild missing cache content. Do not pass the old catalog directory as
the new cache root, or plect will look for catalogs under `catalogs/catalogs/`.

## Verify

Start plect with the new release and run `plect catalog list`. Confirm that
catalogs resolve from the intended cache root. Remove the backup of the
override configuration after verification.
