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
sure nothing will call `POST/DELETE /subscribe` mid-conversion) and copy
the current file to a timestamped backup directory:

```bash
STATE_DIR="${XDG_STATE_HOME:-$HOME/.local/state}/slack-adapter"
BACKUP_DIR="$STATE_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
cp "$STATE_DIR/subscribers.json" "$BACKUP_DIR/subscribers.json" 2>/dev/null
```

## Conversion

Wrap the existing array under a `subscribers` key. There is nothing to
populate `tombstones` with — no thread was ever unsubscribed under the new
binary yet — so it is omitted (an absent key decodes as an empty list):

```bash
jq '{subscribers: .}' "$STATE_DIR/subscribers.json" > "$STATE_DIR/subscribers.json.new" \
  && mv "$STATE_DIR/subscribers.json.new" "$STATE_DIR/subscribers.json"
```

If the file does not exist (no subscriptions registered yet), skip this
step — there is nothing to migrate.

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
