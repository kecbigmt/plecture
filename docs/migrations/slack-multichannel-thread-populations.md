# Migrate Slack service channels and thread populations

The Slack service config uses `channel_ids` arrays. The old `channel_id` key is
rejected. One App declaration may cover several channels and opens one Socket
Mode connection. The thread resource again supports subscribe-only
populations.

## Back up

Stop the resident service. Back up the active config home, including service
declarations and the private token env file, and the data home, including the
Slack plugin's recovery and denial records. Keep both backups outside the
active homes until the new service has been verified. A pre-change binary
requires the backed-up config because it does not read `channel_ids`.

## Update the declaration

Replace each bot entry's `channel_id = "C0123"` with
`channel_ids = ["C0123"]`. If several entries use the same App, combine their
channels into one entry and keep one set of App and bot token names. The list
must be nonempty and unique, and channels must not overlap across entries.
The combined entry has one `allowed_user_ids` and one set of denial messages;
adjust them deliberately when the old per-channel entries differed. Verify
that the App is a member of each channel and has the needed Slack permissions.

For a thread population, choose a private Unix socket path accessible to the
resident service and its query action. Add it once under `[services.config]`:

```toml
[services.config]
socket_path = "/run/plect/slack-mentions.sock"

[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN"
bot_token_env = "SLACK_BOT_TOKEN"
channel_ids = ["C0123", "C0456"]
allowed_user_ids = []
```

Declare the same path and the population-covered subset in the workflow's
query. The query's channel list is the single routing declaration. Mentions
in other allowed channels continue to use the bot-resource subscriber.

```toml
[[thread_worker.populations]]
name = "mentions"
uses = ["subscribe"]
expire_after = "1h"
auto_down = true
auto_destroy = true

[thread_worker.populations.query]
socket_path = "/run/plect/slack-mentions.sock"
channel_ids = ["C0123"]

[thread_worker.populations.session.inputs]
channel_id = { from = "item.channel_id" }
thread_ts = { from = "item.thread_ts" }
```

The containing workflow uses `official.slack.thread` as its entry resource and
declares those session inputs in its input schema. No state rewrite is needed
for existing sessions; core subscriptions remain authoritative.

## Verify

Start the resident service and check that it accepts the service declaration
and population query snapshot. Mention the App in a covered channel and
confirm that one thread session receives the mention and transcript. Mention
it in an allowed, uncovered channel and confirm delivery to the bot-resource
subscriber. Post replies from both channels. Check the service log for channel
overlap, permission, or recovery errors. A service crash after core accepts an
event but before the adapter records success can replay that event; the backup
retains the prior state for rollback.

Custom resident service executables must also accept a `queries` snapshot
after the initial `subscriptions` snapshot and replacement snapshots later.
The rows contain only subscribe populations whose query action uses that
executable; a service without such a query receives an empty snapshot and
may ignore it. Back up its executable and state with the config and data
homes before upgrading it. The shipped GitHub watcher ignores this message.
