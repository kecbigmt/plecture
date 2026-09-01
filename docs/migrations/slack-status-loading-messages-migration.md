# slack-adapter `status_loading_messages` removal

The `status_loading_messages` key in `slack-adapter`'s
`~/.config/slack-adapter/config.toml` supplied the text for the shimmer
status line `slack-adapter` used to set on every inbound message or
app-mention, before delivery to the runtime could be confirmed. That
receipt-time shimmer is removed outright: it asserted progress the adapter
had not actually observed, and a runtime that turned out to be unreachable
left it showing for the full `status_ttl` before silently clearing. The
shimmer is now driven entirely by `POST /status` (the `status` channel
bound to a live runtime's own `plect.status_message` reports), which takes
its `loading_messages` per event rather than from static config.

The change is intentionally breaking. Plecture is pre-1.0, so operators
migrate their own configuration once instead of relying on a compatibility
read: a `config.toml` still declaring `status_loading_messages` fails
`slack-adapter` startup with an error naming the key, rather than the key
being silently ignored.

## Backup

```bash
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
cp ~/.config/slack-adapter/config.toml ~/.config/slack-adapter/config.toml.migration-backup.$STAMP
```

## Find the declaration

```bash
grep -n '^[[:space:]]*status_loading_messages[[:space:]]*=' ~/.config/slack-adapter/config.toml
```

No output means nothing to migrate.

## Delete the key

Before:

```toml
slack_bot_token = "xoxb-..."
status_loading_messages = ["Checking…"]
status_ttl = "15m"
```

After:

```toml
slack_bot_token = "xoxb-..."
status_ttl = "15m"
```

`status_ttl` and every other key are unaffected — only
`status_loading_messages` is retired.

## Verify

Re-run the grep from [Find the declaration](#find-the-declaration): no
hits is the completion condition. Then restart `slack-adapter` (or run
`plect serve` if it manages the service) and confirm it starts without the
"config.toml uses retired key(s)" error.
