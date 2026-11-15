# slack-adapter

`slack-adapter serve` reads the operator service configuration and subscription snapshot from stdin, then opens one Socket Mode connection per bot. It writes resource-addressed publish requests to stdout and logs to stderr. Plect replies with a delivered count or error for each request. Subscription membership stays in core; the adapter has no subscriber ledger, instance registry, HTTP server, or CLI call back into plect.

The service configuration contains a `bots` array. Each bot has `app_token_env`, `bot_token_env`, `channel_id`, and `allowed_user_ids`. The named environment variables hold the credentials. Optional denial reply messages can be configured per bot. `PLECT_DATA_DIR/denials.json` records replies already sent, so a repeated denied mention does not post again.

The same executable provides `resource observe`, `thread create`, `message post`, and `status set` actions. Plect explicitly passes validated `service.config` to each outbound action; the executable uses the bot token associated with the requested channel. Actions call the Slack Web API and do not use a local service endpoint.

Run `go test ./...` in this module to test config validation, protocol correlation, routing, and Slack API actions with local test servers.
