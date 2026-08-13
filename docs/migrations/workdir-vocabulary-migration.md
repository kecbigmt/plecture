# Workdir vocabulary migration

This migration rewrites persisted state and config vocabulary from the old
worktree names to the current workdir names:

- `state.json` session field `worktree_path` becomes `workdir_path`.
- `config.toml` key `worktrees_root` becomes `workdirs_root`.
- Render template variable `{{.WorktreePath}}` becomes
  `{{.WorkdirPath}}`.
- `plect template render --repo <owner/repo>` becomes
  `plect template render --workdir <path>`. The new flag is a filesystem
  path used to search that workdir's `.plect/templates/`; it does not resolve
  or infer a repository slug.
- Error code `workspace_not_found` becomes `session_not_found`, and JSON
  fields named `worktree_path` become `workdir_path`.
- The `git_dirty` list/status field is removed. Workflow authors that need
  this fact should declare an explicit dynamic output in the relevant task.

The embedded default templates were removed. The built-in `investigate`,
`respond`, `review`, and `work` template bodies can be recovered from commit
`66a810d91afbc22eddb4fa1e79ff3573e9c56ac2` if you want to copy them into
`~/.config/plect/templates/` or a workdir's `.plect/templates/` directory:

```bash
git show 66a810d91afbc22eddb4fa1e79ff3573e9c56ac2:app/internal/template/defaults/investigate.md
git show 66a810d91afbc22eddb4fa1e79ff3573e9c56ac2:app/internal/template/defaults/respond.md
git show 66a810d91afbc22eddb4fa1e79ff3573e9c56ac2:app/internal/template/defaults/review.md
git show 66a810d91afbc22eddb4fa1e79ff3573e9c56ac2:app/internal/template/defaults/work.md
```

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
