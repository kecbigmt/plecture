# Move resource subscriptions into core

This procedure moves resource delivery membership into `storage.db`. It removes
resource `subscribe` and `unsubscribe` actions, the delivery retry queue, and
plugin subscription ledgers as delivery authorities. Stop plect, its services,
and plugin daemons before starting. Keep them stopped until verification is
complete.

## Backup and manifest

Record the data home and the paths to every existing GitHub watcher
`subscriptions.json` and Slack adapter `subscribers.json`. Write a manifest:

```json
{
  "github_ledgers": ["/path/to/github-watcher/subscriptions.json"],
  "slack_ledgers": [
    {
      "path": "/path/to/slack-adapter/subscribers.json",
      "team_id": "T0123",
      "bot_user_id": "U0123",
      "workspace_host": "example.slack.com"
    }
  ]
}
```

Include one Slack entry per adapter state directory. The team, bot, and
workspace host identify mention and thread resources: the old ledger stores
only the channel and thread timestamp. Mention memberships resolve to the
`slack://<team>/<bot>` bot-user declaration supplied by the Slack adapter
change stacked on this PR. That declaration owns their type, name, and
subscriber limit. Apply the coordinated adapter change before running this
migration. Use empty arrays when no ledger exists.

Run the one-time importer from a checkout of this release, with the installed
`plect` binary available on `PATH`:

```sh
python3 scripts/migrate-core-subscriptions.py \
  --data-home /path/to/plect-data \
  --manifest /path/to/subscription-manifest.json
```

The importer first makes an online backup of `storage.db` and copies the
manifest, ledgers, and any `pending_delivery.json` into
`<data-home>/subscription-migration-backups/<timestamp>/`. It then runs
`plect --data-home <data-home> storage migrate`. The schema migration maps
live entry, task, node, and pending `subscription_retries` rows into the new
table. The importer then resolves resource declarations for the plugin ledgers
and live `pending_delivery.json` subscribe intents in one database transaction.
The installed `plect` binary must contain the resource declarations used by
those sessions, including trusted project resources. Unsubscribe intents and
subscriptions for destroyed sessions are dropped. A membership that already
has a dispatch, task, or node source does not gain an extra runtime source.
If an existing database membership cannot resolve to a declaration, the
transaction stops so its stored binding is left intact.
Missing sessions, unresolved resource declarations, and memberships beyond
`max_subscribers` are reported as `Dropped:` lines; review each reason before
restarting.

## Verify and recover

Run `plect --data-home /path/to/plect-data subscription list --json` and
compare the live session and resource pairs with the manifest's ledgers.
Publish a harmless event to a test resource with `plect event publish
--resource <id> --type <type> --summary <text>` and check that it appears
once per subscribed session. Remove the old ledger files from the active
plugin state directories only after the new services are running against core
membership. Remove `pending_delivery.json` and its lock file from the active
data home after verifying the imported subscriptions; the backup retains the
pre-migration copy.

If verification fails, keep services stopped. The timestamped backup contains
the pre-migration database and source ledgers. Restore it with the
`plect storage restore` procedure for that release, restore the copied ledger
files to their original paths, then restart the old release. Do not place a
backup database under a newer binary without restoring its matching release.
