# Plugin `config/` layout migration

This migration covers the plugin package format change from
`docs/design/plugin-packaging.md`'s Package format section: a plugin's
config declarations (`providers/`, `resources/`, `channels/`, `tasks/`,
`workflows/`, `templates/`) now live under a `config/` subdirectory instead
of directly at the plugin root. The loader now mounts a plugin's `config/`
as the layer root; a plugin still laid out the old, flat way is invisible
to every per-kind loader (`plect provider show`, `plect workflow show`,
session dispatch, and so on all silently see zero definitions from it).

This repository's own shipped catalog (`github`, `okf`, `session/runtime`)
is already migrated as part of this change. This migration is only for a
plugin directory referenced by a hand-authored `plugin_dirs` entry in your
own `config.toml` — a catalog-mounted plugin (via `catalogs.toml` /
`plect.lock`) is unaffected here, since re-running `plect plugin update`
against a catalog that has migrated its own plugins picks up their new
layout automatically.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate a hand-authored plugin directory once instead of relying on a
compatibility shim that reads both layouts.

## Backup

Before moving anything, copy each affected plugin directory to a
timestamped backup:

```bash
for PLUGIN_DIR in /path/to/your/plugin/dirs/*; do
  BACKUP_DIR="$PLUGIN_DIR.migration-backup.$(date -u +%Y%m%dT%H%M%SZ)"
  cp -r "$PLUGIN_DIR" "$BACKUP_DIR"
done
```

Substitute your actual `plugin_dirs` paths (from `~/.config/plect/config.toml`,
or whatever directory `PLECT_CONFIG_HOME`/`--config-home` points at) for the
glob above.

## Move config declarations under `config/`

For each plugin directory, move only the standard config-kind
subdirectories that exist — `src/`, `bin/`, `scripts/`, `plugin.toml`, and
`README.md` (if present) stay exactly where they are:

```bash
PLUGIN_DIR=/path/to/your/plugin
mkdir -p "$PLUGIN_DIR/config"
for KIND in providers resources channels tasks workflows templates; do
  [ -d "$PLUGIN_DIR/$KIND" ] && mv "$PLUGIN_DIR/$KIND" "$PLUGIN_DIR/config/$KIND"
done
```

No `plugin.toml` change is needed: `[[executables]]` and `[[services]]`
paths are relative to the plugin root, not to the moved directories, so a
`build` command pointing at `src/`/`../bin/` is unaffected by this move.

If the plugin also declares a *legacy* `chains/*.toml` directory (the
already-retired dual-read this repository dropped separately), leave it
where it is — that convention was never part of the `config/` layout and
this migration does not touch it.

## Verification

After migration, confirm the loader actually sees the moved content:

```bash
plect provider list
plect resource list
plect workflow list
plect template list
```

Each command should show the same definitions it did before the move (same
ids), now resolved from `config/<kind>/` instead of `<kind>/` — a
definition that silently disappears from one of these lists means its
subdirectory either wasn't moved or was moved to the wrong place.

## Rollback

Restore each plugin directory from its backup:

```bash
rm -rf "$PLUGIN_DIR"
mv "$PLUGIN_DIR.migration-backup.<timestamp>" "$PLUGIN_DIR"
```

Use a plect binary built before this change — the restored flat layout is
invisible to a post-migration binary's loader.
