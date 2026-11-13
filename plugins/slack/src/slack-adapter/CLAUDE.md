# slack-adapter — Developer Guide

The resident service receives Slack Socket Mode events for every configured bot and publishes to core subscriptions through the private service stdio protocol. Keep provider names and credentials in the plugin. The service logs to stderr; stdout is reserved for protocol requests.

An action invocation is a separate process. It receives the service's env-file entries and only the config explicitly bound in its args. It selects a bot by channel ID and calls the Slack Web API directly. It does not receive `PLECT_DATA_DIR`; the resident service uses that directory only for once-per-denial records.

Test from this module with `go test ./...`. Tests use local Slack API servers and synthetic tokens.
