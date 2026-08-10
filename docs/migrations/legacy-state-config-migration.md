# Legacy state/config migration

`sennit migrate` rewrites `state.json` and `config.toml` from the legacy
forms produced by earlier sennit releases into the current forms, ahead of
the follow-up changes that remove the code which still reads the legacy
forms. Run it once per data directory before upgrading past that removal.

## What is migrated

### state.json

- **Legacy Slack field.** A session's top-level `slack` object (`thread_ts`,
  `channel_id`) is converted into `conversation` (`source: "Slack"` plus
  the same values under `metadata`), then removed.
- **Legacy effects map.** A session's `effects` map is merged into `tasks`.
  Each entry's `effect_id` becomes the corresponding task's `task_id`
  (dropped if a `task_id` is already present), and `effects` is removed.
  An entry whose key already exists in `tasks` is left alone — a real write
  under that key always wins over a stale legacy shadow.
- **Legacy GitHub identity fields.** `url`, `url_type`, `owner_repo`, and
  `number` are folded into `resource_id` (from `url`, if `resource_id` is
  not already set) and `alias` (from `resource_id`, if `alias` is not
  already set), then removed. Every pre-v3 session's `url` was a GitHub
  issue/PR URL and doubled as both the canonical resource id and the
  original create-time input, so this fold is lossless.
- **Legacy inline-task sessions.** A session with no `workflow` was created
  through the retired inline `[[tasks]]` config path, which never had a
  separate task-id concept — the task's map key was its only identity. For
  such a session, every task entry with no explicit `task_id` gets one set
  to its map key. This makes the record self-describing once the "empty
  `task_id` means node id == task id" round-tripping convention is removed
  from the task package, instead of depending on an implicit convention
  that will no longer be documented anywhere in the code.

### config.toml

- **`repo_allowlist`.** Each `"owner/repo"` entry is translated into the
  equivalent `resource_allowlist` regex pattern
  (`^https://github\.com/<owner>/<repo>(/|$)`, with `owner`/`repo`
  regex-escaped) and appended to `resource_allowlist` if not already
  present. `repo_allowlist` is then removed, even if it was present but
  empty.

### Out of scope: `template render --url`

The issue also names `sennit template render`'s `--url` flag. That flag is
a CLI invocation argument, not persisted data — there is no "old form" of
it sitting in a data directory for a migration script to rewrite. Removing
the flag itself is left to the follow-up PR that deletes the legacy
compatibility code from `app/commands/template.go`; this migration only
covers persisted state/config forms.

## Backup

Before rewriting anything, `sennit migrate` copies the current
`state.json` and `config.toml` byte-for-byte into a new timestamped
subdirectory of `<data-dir>/migration-backups/` (e.g.
`migration-backups/20260101T000000.000000000/`). A run that changes
nothing (data already in the current form) creates no backup.

## Running it

```bash
sennit migrate
```

By default this reads/writes `state.json` under `$XDG_DATA_HOME/sennit`
(or `~/.local/share/sennit`) and `config.toml` at
`~/.config/sennit/config.toml`. Override either location with
`--data-dir <dir>` or `--config <path>`.

The command prints `nothing to do: ...` when both files are already in the
current form, or the backup directory path plus one line per change
applied otherwise.

## Verification

After running, confirm the rewritten files parse and look as expected:

```bash
sennit migrate            # re-run: should print "nothing to do"
jq . "$XDG_DATA_HOME/sennit/state.json" | grep -E 'url"|url_type|owner_repo|effects|"slack"'
# ^ should print nothing — no legacy keys remain
grep repo_allowlist ~/.config/sennit/config.toml
# ^ should print nothing
```

Then exercise the normal `sennit ls` / `sennit status` commands against the
migrated data directory and confirm sessions still resolve correctly.

## Rollback

If the migrated data needs to be reverted, copy the backed-up files back
over the current ones:

```bash
BACKUP_DIR=<path printed by the migrate run, e.g. .../migration-backups/20260101T000000.000000000>
cp "$BACKUP_DIR/state.json" "$XDG_DATA_HOME/sennit/state.json"
cp "$BACKUP_DIR/config.toml" ~/.config/sennit/config.toml
```

This restores both files byte-identical to their pre-migration state. No
sennit process should be running against the data directory while the
rollback is applied, since a concurrent write could otherwise interleave
with the copy.
