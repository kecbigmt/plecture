# slack-adapter

`slack-adapter serve` reads the operator service configuration, subscription snapshot, and population query snapshot from stdin, then opens one Socket Mode connection per App. It writes resource-addressed publish requests to stdout and logs to stderr. plect replies with a delivered count or error for each request. Subscription membership stays in core; the adapter has no subscriber ledger, instance registry, HTTP server, or CLI call back into plect.

The service configuration contains a `bots` array. Each bot has `app_token_env`, `bot_token_env`, nonempty `channel_ids`, and `allowed_user_ids`. The named environment variables hold the credentials. Optional denial reply messages can be configured per bot. `socket_path` names a private Unix socket when a thread population consumes appearances. `PLECT_DATA_DIR/denials.json` records replies already sent, so a repeated denied mention does not post again. `PLECT_DATA_DIR/recovery.json` stores restart scan boundaries and processed mention timestamps per channel. `PLECT_DATA_DIR/pending-mentions.json` holds unbound mentions until a thread subscriber is visible and a publish succeeds.

The same executable provides `resource observe`, `thread create`, `message post`, and `status set` actions. Plect explicitly passes validated `service.config` to each outbound action; the executable uses the bot token associated with the requested channel. Actions call the Slack Web API and do not use a local service endpoint.

Run `go test ./...` in this module to test config validation, protocol correlation, routing, and Slack API actions with local test servers.
