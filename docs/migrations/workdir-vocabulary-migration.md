# Workdir vocabulary migration

This migration rewrites persisted state and config vocabulary from the old
worktree names to the current workdir names:

- `state.json` session field `worktree_path` becomes `workdir_path`.
- `config.toml` key `worktrees_root` becomes `workdirs_root`.

Run the one-time migration tool before using a binary built after this
change:

```bash
go run ./plugins/legacy-migration/cmd/legacy-migration
```

Before rewriting anything, the tool copies existing files to a timestamped
backup directory under the data directory's `migration-backups/` directory.
The command prints that backup path when it changes a file.

Rollback:

```bash
cp "$BACKUP_DIR/state.json" "${XDG_DATA_HOME:-$HOME/.local/share}/plect/state.json"
cp "$BACKUP_DIR/config.toml" "$HOME/.config/plect/config.toml"
```

If the command prints `nothing to do`, the files are already in the current
shape and no backup is created.
