# Workspace provider vocabulary migration

This migration covers the rename decided in
`docs/adr/2026-08-17-workspace-provider-vocabulary.md`: the core concept
named `provider` becomes **workspace provider**, and every surface that
named the old vocabulary — config directories, workflow fields, state.json
fields, setup output keys, CLI flags, and shipped plugin executables —
follows.

This migration **supersedes** the prior workdir-vocabulary migration: it
accepts state and config written in either the oldest (worktree-era) or the
intermediate (workdir-era) form and writes only the current workspace
vocabulary, in one pass. If you never ran the prior migration, or already
did, this procedure covers you either way — there is no need to chain two
rewrites.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate once instead of relying on a compatibility shim that reads more
than one vocabulary.

## Backup

Before running anything, back up your data directory and config home:

```bash
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/plect"
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
cp -r "$DATA_DIR" "$DATA_DIR.migration-backup.$STAMP"
cp -r "$CONFIG_HOME" "$CONFIG_HOME.migration-backup.$STAMP"
```

The `legacy-migration` tool below additionally makes its own timestamped
backup of `state.json`/`config.toml` under `migration-backups/` next to
your data directory before it rewrites anything — the copies above are the
belt to that tool's suspenders, and also cover the config-home directories
it does not touch (see the manual steps below).

## Automated: state.json and config.toml

Run the one-time migration tool before using a binary built after this
change:

```bash
go run ./plugins/legacy-migration/cmd/legacy-migration
```

It rewrites, in one pass, regardless of which prior form your data is in:

- `state.json` session field `worktree_path` or `workdir_path` becomes
  `workspace_dir_path`.
- `state.json` `@workflow` task's setup output key `workdir` becomes
  `workspace_dir`.
- `config.toml` key `worktrees_root` or `workdirs_root` becomes
  `workspace_dirs_root`.

The command prints the backup path (see Backup above) when it changes a
file, and prints `nothing to do` when the files are already in the current
form.

## Manual: config directories and workflow fields

The migration tool only rewrites `state.json` and `config.toml` — it does
not walk arbitrary plugin or workflow directories. Rename these yourself:

**Global `providers/` config directory**, if you have one (a catalog-mounted
plugin's own `providers/` directory is renamed by the catalog itself; see
below):

```bash
CONFIG_HOME="${PLECT_CONFIG_HOME:-$HOME/.config/plect}"
[ -d "$CONFIG_HOME/providers" ] && mv "$CONFIG_HOME/providers" "$CONFIG_HOME/workspaces"
```

**Hand-authored plugin directories** (a `plugin_dirs` entry in
`config.toml`, not a catalog-mounted plugin — `plect plugin update` picks
up a catalog's own rename automatically once the catalog has migrated):

```bash
for PLUGIN_DIR in /path/to/your/plugin/dirs/*; do
  [ -d "$PLUGIN_DIR/config/providers" ] && mv "$PLUGIN_DIR/config/providers" "$PLUGIN_DIR/config/workspaces"
done
```

**Workflow files** declaring the renamed field, under the global
`workflows/` directory and any repo `.plect/workflows/` overlay:

```bash
grep -rlZ '^provider = ' "$CONFIG_HOME/workflows" 2>/dev/null | xargs -0 -r sed -i 's/^provider = /workspace_provider = /'
grep -rlZ '^provider = ' .plect/workflows 2>/dev/null | xargs -0 -r sed -i 's/^provider = /workspace_provider = /'
```

**Custom templates or task definitions** referencing the renamed template
variable (a shipped catalog's own tasks/templates are already updated; this
only matters for anything you authored yourself):

```bash
grep -rlZ '{{\.WorkdirPath}}' "$CONFIG_HOME" .plect 2>/dev/null | xargs -0 -r sed -i 's/{{\.WorkdirPath}}/{{.WorkspaceDirPath}}/g'
```

## CLI flag renames

- `plect template render --workdir <path>` becomes
  `plect template render --workspace-dir <path>`.
- `plect template list --workdir <path>` becomes
  `plect template list --workspace-dir <path>`.
- `plect init --workdirs-root <path>` becomes
  `plect init --workspace-dirs-root <path>`.

## JSON field renames

Any script or agent reading `plect status --json` / `plect create --json` /
`plect up --json` / `plect destroy --json` / `plect ls --json` output must
follow these field renames:

- `workdir_path` becomes `workspace_dir_path`.
- `workdir_exists` becomes `workspace_dir_exists`.
- `reused_workdir` becomes `reused_workspace_dir`.
- `removed_workdir` becomes `removed_workspace_dir`.
- `provider` / `provider_info` (workflow show/detail output) become
  `workspace_provider` / `workspace_provider_info`.

## Verification

After migration, confirm the loader sees the renamed config and the
rewritten state:

```bash
plect workflow list
plect workflow show <id>   # shows "Workspace provider:" when one is declared
plect status <session>     # shows the "workspace dir" runtime field
```

Confirm no core-facing bare `provider` vocabulary or `workdir_path`/
`workdirs_root` remains in your own config:

```bash
grep -rn '^provider = ' "$CONFIG_HOME/workflows" .plect/workflows 2>/dev/null
grep -rln 'workdir_path\|workdirs_root\|worktrees_root' "$CONFIG_HOME" 2>/dev/null
```

Both should produce no output.

## Rollback

Restore state and config from the automated tool's own backup:

```bash
cp "$BACKUP_DIR/state.json" "${XDG_DATA_HOME:-$HOME/.local/share}/plect/state.json"
cp "$BACKUP_DIR/config.toml" "$HOME/.config/plect/config.toml"
```

Restore the config-home and plugin-directory renames from the backups taken
in the Backup step above:

```bash
rm -rf "$CONFIG_HOME"
mv "$CONFIG_HOME.migration-backup.$STAMP" "$CONFIG_HOME"
```

Use a plect binary built before this change — the restored files and
directory layout are invisible to a post-migration binary's loader.
