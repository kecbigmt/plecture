# Slack plugin

The `slack-adapter` service holds one Socket Mode connection per configured bot. Plect owns subscriptions and delivers resource events over the service's private stdio protocol. The adapter has no local HTTP listener or subscriber registry.

Declare one service in `<config-home>/services/slack.toml`:

```toml
[[services]]
name = "slack"
executable = "official/slack/slack-adapter"
env_file = "/absolute/path/to/private/slack.env"
args = ["serve"]

[services.config]
[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_PRIMARY"
bot_token_env = "SLACK_BOT_TOKEN_PRIMARY"
channel_id = "C0123"
allowed_user_ids = ["U0123"]

[[services.config.bots]]
app_token_env = "SLACK_APP_TOKEN_SECONDARY"
bot_token_env = "SLACK_BOT_TOKEN_SECONDARY"
channel_id = "C0456"
allowed_user_ids = []
```

The env file supplies the named tokens as `KEY=VALUE` lines and must be readable only by its owner. The adapter rejects duplicate channel IDs and missing token bindings. An empty `allowed_user_ids` allows everyone. Optional `denied_user_message` and `denied_channel_message` send one reply per bot, channel, thread, and user; empty values silently drop denied mentions.

`slack://<team_id>/<bot_user_id>` is the bot resource and permits one distinct subscriber. A session can take it as its entry resource or subscribe to it with `plect subscribe`. Slack thread permalinks are thread resources. A mention first publishes `user.emit` to its thread resource. Zero delivery tries the bot resource; another zero drops the mention with a log line. Publish errors stop routing so a storage failure cannot masquerade as an empty subscription.

The `slack_thread` effect creates a thread and returns `thread_ts`, `channel_id`, and `permalink`. The `slack`, `status`, and `stream` channels invoke `slack-adapter` actions that call the Slack Web API directly. They take `channel_id` and `thread_ts`, with no `base_url`. The `stream` channel posts complete `plect.message` events; include only that type when wiring it into a workflow. The adapter chooses the bot by channel ID.

Populations still use `poll` or `subscribe` with `[query]` as before. The Slack thread resource no longer has `query.subscribe`, and no population is synthesized from unbound mentions.

See [the migration procedure](../../docs/migrations/slack-multi-bot-service.md) when updating an existing deployment.
