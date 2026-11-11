# Move Slack adapters to one multi-bot service

Stop plect and the old Slack adapter processes. Back up the active config home, data home, and each adapter's `config.toml` and state directory before changing them. Follow the [core subscription migration](core-subscriptions.md) first; its importer copies each old `subscribers.json` into core membership and reports dropped rows.

Create an owner-readable env file containing the existing app and bot tokens under distinct variable names. Create one operator service declaration under `<config-home>/services/` with `executable = "official/slack/slack-adapter"`, `args = ["serve"]`, and one `[[services.config.bots]]` per old adapter. Set each bot's `app_token_env`, `bot_token_env`, `channel_id`, and `allowed_user_ids`. Channel IDs must be unique. If the old deployment sent denial replies, set `denied_user_message` and `denied_channel_message` per bot. Use mode `0600` for the env file.

Replace each workflow `base_url` input and binding with `channel_id` and `thread_ts`. The `slack_thread` effect needs `channel_id` and `root_text`. Channel `stream` accepts complete `plect.message` events; remove `plect.message_delta` from its `include` list. Replace `slack://<team>/<bot>/mentions` resource references with `slack://<team>/<bot>`. Remove Slack thread `query.subscribe` populations and any `subscribe unbound-mentions` command. Populations for other query sources retain their `uses` and `[query]` values.

Start plect and check `plect service list` for the Slack service. Use `plect subscription list --json` to confirm the migrated bot and thread memberships. Send a test mention to a subscribed thread, then a thread without a subscriber while the bot resource has one. Confirm that each reaches the intended session once and that a mention with neither subscriber produces a drop log. Check outbound thread creation, message posting, and status through one channel per bot.

If verification fails, stop the new service and restore the backed-up config and data homes, adapter files, and old release together. Keep the backups until the migration is verified.
