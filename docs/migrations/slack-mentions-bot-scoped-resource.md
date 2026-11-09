# Slack `mentions` resource scoped by bot user ID

The Slack plugin's `mentions` resource changes identity from
`slack://<team_id>/mentions` (named `slack/<team_id>-mentions`) to
`slack://<team_id>/<bot_user_id>/mentions` (named
`slack/<team_id>-<bot_user_id>-mentions`). Several Slack Apps installed in one
workspace share the team ID, so each App's adapter now owns its own resource.
The old identifier no longer matches any declaration.

The adapter's `POST /subscribe` and `DELETE /subscribe` now require the bot
user ID for a mention subscription, and reject one naming another bot. A
running adapter built before this change rejects the new hook's request
body, so restart the adapter whenever the plugin is upgraded.

The ledger format (`subscribers.json`, version 2) is unchanged. A mention
subscription in the ledger is keyed by `(session_name, "mentions")` and
carries no team or bot ID, so existing subscriptions keep delivering after
the upgrade, with their transcript watermarks intact.

## Backup

Stop the adapter, or make sure nothing calls `POST/DELETE /subscribe` while
the copy runs, then copy the ledger and verify the copy byte for byte:

```bash
set -euo pipefail
STATE_DIR="${SLACK_ADAPTER_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/slack-adapter}"
SRC="$STATE_DIR/subscribers.json"
BACKUP_DIR="$STATE_DIR/migration-backups/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
if [ -f "$SRC" ]; then
  cp "$SRC" "$BACKUP_DIR/subscribers.json"
  cmp -s "$SRC" "$BACKUP_DIR/subscribers.json" || {
    echo "backup does not match source, aborting" >&2
    exit 1
  }
fi
```

Also back up any config file or script edited in the steps below.

## Procedure

1. Look up the team ID and bot user ID of the App whose token the adapter
   uses:

   ```bash
   curl -s -H "Authorization: Bearer $SLACK_BOT_TOKEN" https://slack.com/api/auth.test \
     | jq -r '"\(.team_id) \(.user_id)"'
   ```

2. Find every place that names the old identifier, and replace each
   `slack://<team_id>/mentions` with `slack://<team_id>/<bot_user_id>/mentions`:

   ```bash
   grep -rnE 'slack://[A-Z0-9]+/mentions' "${PLECT_CONFIG_HOME:-${XDG_CONFIG_HOME:-$HOME/.config}/plect}"
   ```

   Include any script or unit file outside the plect config home that runs
   `plect subscribe` with the old identifier.

3. Upgrade the plugin (`plect plugin update <catalog>/slack`) and restart the
   adapter.

4. Confirm the adapter registered its address:

   ```bash
   ls "${XDG_STATE_HOME:-$HOME/.local/state}/slack-adapter/instances/"
   ```

   The directory holds `<team_id>-<bot_user_id>.json`. If it is missing, the
   adapter log names the reason (usually a failed `auth.test`).

5. Re-subscribe each session that was subscribed by hand with the old
   identifier, so it is bound under the new identity:

   ```bash
   plect subscribe "slack://<team_id>/<bot_user_id>/mentions" --session <session>
   ```

   The subscription already exists in the ledger under the same key, so this
   keeps its watermarks and does not duplicate delivery.

## Moving to one adapter per App

The existing ledger belongs to the App whose token the existing adapter
uses. Keep that App's adapter on the existing state directory, or move the
directory to the adapter's new `--state-dir` while the adapter is stopped.
Start every other App's adapter with an empty state directory of its own, its
own `--config`, and its own `listen_addr`. Never point two adapters at one
state directory. Then subscribe each orchestrator to its own App's
`mentions` resource.

## Rollback

Stop the adapter, restore `subscribers.json` and the edited files from the
backup, reinstall the previous plugin revision, and start the adapter.
