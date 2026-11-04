# Convert Slack thread subscriptions to resource delivery

The Slack adapter now stores subscriptions by `(session_name, thread_ts)` and publishes inbound Slack events to each session's event log. Its persisted file has version 2 and contains no `socket_path` or watermark tombstones. A version 1 file is rejected at startup. Convert it while the adapter is stopped, before starting the new binary.

## Backup and conversion

Run these commands on the host that stores the adapter state. Stop the adapter first. The backup must compare byte for byte before conversion.

```bash
set -euo pipefail
state_dir="${XDG_STATE_HOME:-$HOME/.local/state}/slack-adapter"
source_file="$state_dir/subscribers.json"
if [ -f "$source_file" ]; then
  backup="$source_file.before-resource-subscriptions"
  cp "$source_file" "$backup"
  cmp "$source_file" "$backup"
  replacement="$source_file.new"
  umask 077
  jq -e '
    if type != "object" or (.subscribers | type) != "array" then error("expected the version 1 subscriber envelope") else . end
    | {version: 2, subscribers: [.subscribers[]
        | select(.session_name != null and .session_name != "" and .thread_ts != null and .channel_id != null)
        | {session_name, thread_ts, channel_id, since, delivered_through}
      ]}
  ' "$backup" > "$replacement"
  mv "$replacement" "$source_file"
fi
```

The conversion keeps each active subscription's watermark and drops socket bindings. The old tombstones do not contain `channel_id`, so they cannot become complete subscriptions from this file alone. Before restarting, list them from the backup:

```bash
jq -r '.tombstones[]? | [.session_name, .thread_ts, .delivered_through] | @tsv' "$backup"
```

For each row whose session still exists, find its Slack permalink in the session's saved resource or `slack_thread` output, then run `plect subscribe <permalink> --session <session_name>` after the new adapter starts. That registers a down session as well as an up one. The old watermark cannot be restored through the subscription CLI; its next mention may repeat earlier transcript lines. Verify the resulting `(session_name, thread_ts)` pair appears in `GET /subscribers`. Rows for destroyed sessions need no registration.

## Verify and roll back

Start the new adapter. `GET /subscribers` should list the converted `(session_name, thread_ts)` pairs, including sessions that are down. Its startup sweep removes entries for sessions that `plect ls --json` no longer lists. Compare with the backup, accounting for destroyed sessions.

To roll back, stop the adapter and restore the verified backup before starting the old binary:

```bash
cp "$source_file.before-resource-subscriptions" "$source_file"
```

Workflows that declare `official.slack.slack_subscribe` must be updated to use the thread resource subscription and `slack_thread` session scoped effect. Rebase any running workflow definition after updating it. The old effect declaration is removed from the plugin.
