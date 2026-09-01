# slack-adapter `subscribers.json` envelope migration

This migration covers slack-adapter's persisted subscription file,
`$XDG_STATE_HOME/slack-adapter/subscribers.json` (default
`~/.local/state/slack-adapter/subscribers.json`). Its top-level shape
changes from a bare `[Subscriber, ...]` array to an envelope object,
`{"subscribers": [...], "tombstones": [...]}`, so that unsubscribed
threads' delivery watermarks (`tombstones`) can persist in the same
tmp→rename snapshot as live subscriptions.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate persisted data once instead of relying on a compatibility shim. A
slack-adapter built after this change treats a bare-array file as corrupt:
it logs a warning, starts with empty subscriptions, and the next
`/subscribe` call repopulates the file in the new shape — subscriptions and
delivery watermarks predating the restart are lost, defeating the exact
redelivery guard this change exists to add. Convert the file before
starting the new binary.

## Backup

Before editing the file, stop the running slack-adapter process (or make
sure nothing will call `POST/DELETE /subscribe` mid-conversion). `set -e`
already stops the script if `cp` itself exits non-zero, but exit status
alone doesn't rule out a truncated or otherwise incomplete copy, so the
backup is also verified byte-for-byte against the source before conversion
is allowed to touch the original file:

```bash
set -euo pipefail
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/slack-adapter"
SRC="$STATE_DIR/subscribers.json"

if [ ! -f "$SRC" ]; then
  echo "no subscribers.json to migrate" >&2
  exit 0
fi

BACKUP_DIR="$STATE_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$SRC" "$BACKUP_DIR/subscribers.json"
cmp -s "$SRC" "$BACKUP_DIR/subscribers.json" || {
  echo "backup does not match source, aborting" >&2
  exit 1
}
```

## Conversion

Wrap the existing array under a `subscribers` key. There is nothing to
populate `tombstones` with — no thread was ever unsubscribed under the new
binary yet — so it is omitted (an absent key decodes as an empty list).
The adapter creates `subscribers.json` mode `0600`; `jq`'s output would
otherwise land under the shell's umask, so the replacement file is put in
place with `0600` before anything is written to it, and `mv` (same
filesystem) carries that mode into the final name rather than the
original file's:

```bash
set -euo pipefail
NEW="$STATE_DIR/subscribers.json.new"
: > "$NEW"
chmod 600 "$NEW"
jq '{subscribers: .}' "$SRC" > "$NEW"
mv "$NEW" "$SRC"
```

`set -euo pipefail` here is not redundant with Backup's: if this block runs
in a shell that never ran Backup, `$SRC` is unset and `-u` aborts
immediately instead of operating on an empty path. With Backup run first
in the same shell, a failing `jq` call (malformed input, for instance)
still stops the script before `mv` replaces `subscribers.json`, leaving
the original file untouched and the verified backup as the recovery path.

## Verification

Start the new slack-adapter binary and confirm every previously registered
thread came back:

```bash
curl -s "$BASE_URL/subscribers" | jq '.[] | {thread_ts, session_name}'
```

Compare the output against the backed-up file's entries. The adapter's
startup log line (`restored subscribers`) should report the same
subscriber count as the pre-migration file held.

## Rollback

Stop slack-adapter, then restore the backed-up file:

```bash
cp "$BACKUP_DIR/subscribers.json" "$STATE_DIR/subscribers.json"
```

Restart only with a slack-adapter binary built before this change — the
restored file is back in the bare-array shape, which a post-migration
binary rejects as corrupt.
